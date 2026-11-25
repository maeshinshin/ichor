/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// convert reads Ichor's configuration from JSON, as the API server hands it over, converts it and returns the
// Talos documents the real machinery types make of it.
func convert(t *testing.T, raw string, multidoc bool) []config.Document {
	t.Helper()

	var c ichorconfig.Config

	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}

	docs, err := ConvertConfig(&c, multidoc)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]config.Document, 0, len(docs))

	for _, d := range docs {
		doc, err := d.Document()
		if err != nil {
			t.Fatal(err)
		}

		out = append(out, doc)
	}

	return out
}

func only[T config.Document](t *testing.T, docs []config.Document) T {
	t.Helper()

	var found []T

	for _, d := range docs {
		if v, ok := d.(T); ok {
			found = append(found, v)
		}
	}

	if len(found) != 1 {
		t.Fatalf("want one %T among %d documents, got %d", *new(T), len(docs), len(found))
	}

	return found[0]
}

func TestConfigSettingReachesTheNodesAsTheTalosDocument(t *testing.T) {
	docs := convert(t, `{
		"machine": {
			"install": {"disk": "/dev/vda"},
			"sysctls": {"net.ipv4.ip_forward": "1"},
			"kubelet": {"extraArgs": {"v": ["4"]}},
			"time": {"enabled": false, "ntp": {"servers": ["time.example.org"]}}
		},
		"cluster": {"network": {"dnsDomain": "cluster.local", "podSubnets": ["10.244.0.0/16"], "serviceSubnets": ["10.96.0.0/12"]}}
	}`, true)

	if got := only[*mv1alpha1.Config](t, docs).MachineConfig.MachineInstall.InstallDisk; got != "/dev/vda" { //nolint:staticcheck // the field is what is being checked
		t.Errorf("machine.install.disk reached the v1alpha1 document as %q", got)
	}

	if got := only[*runtime.SysctlConfigV1Alpha1](t, docs).Params[testSysctlKey]; got != "1" {
		t.Errorf("machine.sysctls reached SysctlConfig as %q", got)
	}

	kubelet, err := encoder.NewEncoder(only[*k8s.KubeletConfigV1Alpha1](t, docs)).Encode()
	if err != nil || !strings.Contains(string(kubelet), "extraArgs:\n    v:") {
		t.Errorf("machine.kubelet.extraArgs reached KubeletConfig as %s (%v)", kubelet, err)
	}

	ts := only[*network.TimeSyncConfigV1Alpha1](t, docs)
	if ts.TimeEnabled == nil || *ts.TimeEnabled || ts.TimeNTP == nil || ts.TimeNTP.Servers[0] != "time.example.org" {
		t.Errorf("machine.time reached TimeSyncConfig as %+v", ts)
	}

	if got := only[*k8s.KubeNetworkConfigV1Alpha1](t, docs).NetworkPodSubnets; len(got) != 1 || got[0].String() != "10.244.0.0/16" {
		t.Errorf("cluster.network.podSubnets reached KubeNetworkConfig as %v", got)
	}
}

// Talos spells "switch the CoreDNS deployment off" as disabled in the v1alpha1 document and as enabled: false in
// its own document, and spells "switch time sync off" the same two ways. Ichor has the document's enabled alone.
func TestSettingTalosSpellsTwoWaysIsWrittenOnceAndConvertsToTheSpellingTalosValidates(t *testing.T) {
	docs := convert(t, `{"cluster": {"coreDNS": {"enabled": false}}, "machine": {"time": {"enabled": false}}}`, true)

	dns := only[*k8s.KubeCoreDNSConfigV1Alpha1](t, docs)
	if dns.PodEnabled == nil || *dns.PodEnabled {
		t.Fatalf("cluster.coreDNS.enabled=false reached KubeCoreDNSConfig as %v", dns.PodEnabled)
	}

	for _, d := range docs {
		if c, ok := d.(*mv1alpha1.Config); ok {
			t.Fatalf("a v1alpha1 document was written as well: %+v", c)
		}

		if v, ok := d.(config.Validator); ok {
			if _, err := v.Validate(runtimeMode{}); err != nil {
				t.Errorf("%T does not validate: %v", d, err)
			}
		}
	}

	if _, ok := reflect.TypeFor[ichorconfig.ClusterCoreDNS]().FieldByName("Disabled"); ok {
		t.Errorf("the schema carries the second spelling of cluster.coreDNS.enabled")
	}

	if _, ok := reflect.TypeFor[ichorconfig.MachineTime]().FieldByName("Disabled"); ok {
		t.Errorf("the schema carries the second spelling of machine.time.enabled")
	}
}

type runtimeMode struct{}

func (runtimeMode) String() string        { return "test" }
func (runtimeMode) RequiresInstall() bool { return false }
func (runtimeMode) InContainer() bool     { return false }

func TestRepeatableNamedThingCanBeWrittenTwice(t *testing.T) {
	docs := convert(t, `{"machine": {"kernel": {"modules": {"nbd": {}, "br_netfilter": {"parameters": ["nf_conntrack_helper=1"]}}}}}`, true)

	got := map[string][]string{}

	for _, d := range docs {
		m, ok := d.(*runtime.KernelModuleConfigV1Alpha1)
		if !ok {
			t.Fatalf("unexpected document %T", d)
		}

		got[m.MetaName] = m.ModuleParameters
	}

	if len(got) != 2 || len(got["br_netfilter"]) != 1 {
		t.Errorf("want nbd and br_netfilter as two documents, got %v", got)
	}
}

func TestKubernetesDocumentsNeedAMachineThatTakesThem(t *testing.T) {
	// A setting both spell goes to the v1alpha1 document where the machine takes no Kubernetes document.
	docs := convert(t, `{"cluster": {"network": {"podSubnets": ["10.244.0.0/16"]}}}`, false)
	if got := only[*mv1alpha1.Config](t, docs).ClusterConfig.ClusterNetwork.PodSubnet; len(got) != 1 { //nolint:staticcheck // the field is what is being checked
		t.Errorf("podSubnets reached the v1alpha1 document as %v", got)
	}

	// A setting only the document spells cannot be written there.
	var c ichorconfig.Config

	if err := json.Unmarshal([]byte(`{"cluster": {"coreDNS": {"enabled": false}}}`), &c); err != nil {
		t.Fatal(err)
	}

	if _, err := ConvertConfig(&c, false); err == nil || !strings.Contains(err.Error(), "Talos 1.14") {
		t.Errorf("want a refusal that names the Talos version, got %v", err)
	}
}

// A machine that takes no Kubernetes documents gets the settings the v1alpha1 document has a list for there, and
// is refused the ones it has not.
func TestKubernetesCollectionsOnAMachineThatTakesNoKubernetesDocuments(t *testing.T) {
	docs := convert(t, `{"cluster": {"inlineManifests": {"b": {"manifest": "kind: B"}, "a": {"manifest": "kind: A"}},
		"extraManifests": {"x": {"url": "https://example.invalid/x.yaml"}}}}`, false)

	//nolint:staticcheck // the fields are what is being checked
	cfg := only[*mv1alpha1.Config](t, docs).ClusterConfig
	//nolint:staticcheck // the fields are what is being checked
	if len(docs) != 1 || len(cfg.ClusterInlineManifests) != 2 || cfg.ClusterInlineManifests[0].InlineManifestName != "a" ||
		len(cfg.ExtraManifests) != 1 {
		t.Errorf("want one v1alpha1 document holding both manifests, got %d documents, %+v", len(docs), cfg)
	}

	var c ichorconfig.Config

	if err := json.Unmarshal([]byte(`{"machine": {"staticPods": {"p": {}}}}`), &c); err != nil {
		t.Fatal(err)
	}

	if _, err := ConvertConfig(&c, false); err == nil || !strings.Contains(err.Error(), "Talos 1.14") {
		t.Errorf("want a refusal that names the Talos version, got %v", err)
	}
}

func TestSpecWithoutConfigCarriesNoDocuments(t *testing.T) {
	docs, err := (&TalosConfigPatchSpec{}).EffectiveDocuments(true)
	if err != nil || len(docs) != 0 {
		t.Errorf("want no documents, got %v (%v)", docs, err)
	}
}

// The documents the CLI and the UI build are written in spec.config, and read back they give the same documents.
func TestConfigFromDocumentsConvertsBackToTheDocuments(t *testing.T) {
	want := []TalosConfigPatchDocument{
		{Kind: kindSysctl, SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{testSysctlKey: "1"}}},
		{Kind: kindKernelModule, KernelModuleConfig: &typedconfig.KernelModuleConfigV1Alpha1{MetaName: "nbd"}},
		{Kind: kindKernelModule, KernelModuleConfig: &typedconfig.KernelModuleConfigV1Alpha1{MetaName: "br_netfilter", ModuleParameters: []string{"nf_conntrack_helper=1"}}},
		{Kind: "UserVolumeConfig", UserVolumeConfig: &typedconfig.UserVolumeConfigV1Alpha1{
			MetaName:         "scratch",
			ProvisioningSpec: &typedconfig.ProvisioningSpec{DiskSelectorSpec: &typedconfig.DiskSelector{Match: `disk.wwid == "x"`}, ProvisioningMinSize: "0"},
		}},
		{Kind: configKind, Config: &typedconfig.Config{
			MachineConfig: &typedconfig.MachineConfig{MachineType: "controlplane", MachineCertSANs: []string{epIP}},
		}},
	}

	c, err := ConfigFromDocuments(want)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ConvertConfig(c, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(want) {
		t.Fatalf("want %d documents, got %d", len(want), len(got))
	}

	if c.Machine.Kernel.Modules["br_netfilter"].Parameters[0] != "nf_conntrack_helper=1" || c.Machine.Sysctls[testSysctlKey] != "1" {
		t.Errorf("settings are not where the schema has them: %+v", c.Machine)
	}
}

// A setting the schema does not carry is refused by name, never dropped: a document that spells in the v1alpha1
// document a setting another document spells is one case.
func TestConfigFromDocumentsRefusesWhatTheSchemaDoesNotCarry(t *testing.T) {
	_, err := ConfigFromDocuments([]TalosConfigPatchDocument{{Kind: configKind, Config: &typedconfig.Config{
		ClusterConfig: &typedconfig.ClusterConfig{ExtraManifests: []string{"https://example.invalid/x.yaml"}},
	}}})

	if err == nil || !strings.Contains(err.Error(), "cluster extraManifests") {
		t.Fatalf("want a refusal naming cluster extraManifests, got %v", err)
	}

	if _, err := ConfigFromDocuments([]TalosConfigPatchDocument{{Kind: "NoSuchKind"}}); err == nil {
		t.Fatal("want an unknown kind refused")
	}
}

// A document switched off has no document to hold its other settings: they go to the v1alpha1 document with the
// switch, so that Talos does not see the one setting in both.
func TestSwitchedOffDocumentKeepsItsSettingsInTheV1Alpha1Document(t *testing.T) {
	docs := convert(t, `{"machine": {"features": {"kubernetesTalosAPIAccess": {"enabled": false, "allowedRoles": ["os:reader"]}}}}`, true)

	if len(docs) != 1 {
		t.Fatalf("want the v1alpha1 document alone, got %d documents", len(docs))
	}

	access := only[*mv1alpha1.Config](t, docs).MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig //nolint:staticcheck // the field is what is being checked
	if access.AccessEnabled == nil || *access.AccessEnabled || len(access.AccessAllowedRoles) != 1 {
		t.Errorf("the v1alpha1 document holds %+v", access)
	}
}

// Two v1alpha1 extraHostEntries of one address, and two kernel modules of one name, are valid there: the settings
// carry them as one entry whose items join in order, each once.
func TestDuplicateV1Alpha1EntriesMerge(t *testing.T) {
	docs := []TalosConfigPatchDocument{}
	raw := `[{"kind":"Config","config":{"version":"v1alpha1","machine":{` +
		`"network":{"extraHostEntries":[{"ip":"10.1.1.1","aliases":["a.example","b.example"]},` +
		`{"ip":"10.1.1.1","aliases":["b.example","c.example"]}]},` +
		`"kernel":{"modules":[{"name":"m","parameters":["x=1"]},{"name":"m","parameters":["x=1","y=2"]}]}}}}]`

	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		t.Fatal(err)
	}

	cfg, err := ConfigFromDocuments(docs)
	if err != nil {
		t.Fatalf("ConfigFromDocuments: %v", err)
	}

	b, _ := json.Marshal(cfg)

	for _, want := range []string{`"10.1.1.1":{"hostnames":["a.example","b.example","c.example"]}`, `"parameters":["x=1","y=2"]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %s", b, want)
		}
	}
}
