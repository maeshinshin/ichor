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

package cilium

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart/loader"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// object is the part of a rendered manifest these tests look at.
type object struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Data map[string]string `json:"data"`
}

func renderObjects(t *testing.T, p Preset, mode cni.KubeProxyMode) (string, []object) {
	t.Helper()

	out, err := p.BootstrapManifest(mode)
	if err != nil {
		t.Fatalf("BootstrapManifest(%s): %v", mode, err)
	}

	var objs []object

	for doc := range strings.SplitSeq(string(out), "\n---\n") {
		doc = strings.TrimPrefix(doc, "---\n")
		if strings.TrimSpace(doc) == "" {
			continue
		}

		var o object
		if err := sigsyaml.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatalf("rendered document is not YAML: %v\n%s", err, doc)
		}

		objs = append(objs, o)
	}

	return string(out), objs
}

func find(objs []object, kind, name string) *object {
	for i := range objs {
		if objs[i].Kind == kind && objs[i].Metadata.Name == name {
			return &objs[i]
		}
	}

	return nil
}

func TestBootstrapManifest_IsTheExpandedChart(t *testing.T) {
	_, objs := renderObjects(t, New(), cni.KubeProxyEnabled)

	for _, want := range [][2]string{
		{kindDaemonSet, ChartName},
		{"Deployment", "cilium-operator"},
		{"ConfigMap", "cilium-config"},
		{"ServiceAccount", ChartName},
		{"ClusterRole", ChartName},
	} {
		if find(objs, want[0], want[1]) == nil {
			t.Errorf("rendered manifest has no %s %q", want[0], want[1])
		}
	}

	for _, o := range objs {
		if o.Kind == "" {
			t.Errorf("rendered a document without a kind: %+v", o)
		}
		// Cluster-scoped kinds have no namespace; every namespaced one must be kube-system.
		// cilium-secrets is the chart's own secrets namespace (Roles for TLS interception).
		if ns := o.Metadata.Namespace; ns != "" && ns != namespace && ns != ciliumSecretsNamespace {
			t.Errorf("%s %q is in namespace %q, want kube-system", o.Kind, o.Metadata.Name, o.Metadata.Namespace)
		}
	}
}

func TestBootstrapManifest_AppliesPresetValues(t *testing.T) {
	_, enabled := renderObjects(t, New(), cni.KubeProxyEnabled)
	cm := find(enabled, "ConfigMap", "cilium-config")
	if cm == nil {
		t.Fatal("no cilium-config ConfigMap")
	}
	if got := cm.Data["kube-proxy-replacement"]; got != "false" {
		t.Errorf("kube-proxy-replacement = %q, want false with kube-proxy enabled", got)
	}
	if got := cm.Data["cgroup-root"]; got != "/sys/fs/cgroup" {
		t.Errorf("cgroup-root = %q, want /sys/fs/cgroup", got)
	}

	raw, replaced := renderObjects(t, New(), cni.KubeProxyReplaced)
	cm = find(replaced, "ConfigMap", "cilium-config")
	if cm == nil {
		t.Fatal("no cilium-config ConfigMap")
	}
	if got := cm.Data["kube-proxy-replacement"]; got != "true" {
		t.Errorf("kube-proxy-replacement = %q, want true when replaced", got)
	}
	for _, want := range []string{"KUBERNETES_SERVICE_HOST", "localhost",
		"KUBERNETES_SERVICE_PORT", `"7445"`, "NET_ADMIN"} {
		if !strings.Contains(raw, want) {
			t.Errorf("replaced render missing %q", want)
		}
	}
}

func TestBootstrapManifest_IsDeterministic(t *testing.T) {
	first, err := New().BootstrapManifest(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		again, err := New().BootstrapManifest(cni.KubeProxyReplaced)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("BootstrapManifest differs between runs")
		}
	}
}

func TestBootstrapManifest_TunnelProtocol(t *testing.T) {
	def, _ := renderObjects(t, New(), cni.KubeProxyEnabled)
	_, objs := renderObjects(t, New(), cni.KubeProxyEnabled)
	if got := find(objs, "ConfigMap", "cilium-config").Data["tunnel-protocol"]; got != "vxlan" {
		t.Errorf("default tunnel-protocol = %q, want vxlan", got)
	}

	p, err := New().WithTunnel(tunnelGeneve)
	if err != nil {
		t.Fatal(err)
	}

	geneve, objs := renderObjects(t, p, cni.KubeProxyEnabled)
	if got := find(objs, "ConfigMap", "cilium-config").Data["tunnel-protocol"]; got != tunnelGeneve {
		t.Errorf("tunnel-protocol = %q, want geneve", got)
	}
	if def == geneve {
		t.Error("geneve render equals the default render")
	}

	if _, err := New().WithTunnel("gre"); err == nil {
		t.Error("WithTunnel(gre) succeeded, want an error")
	}
}

// TestBootstrapManifest_FitsInlineManifest keeps the rendered size visible: it
// goes into the machine config, which Talos reads whole.
func TestBootstrapManifest_FitsInlineManifest(t *testing.T) {
	out, err := New().BootstrapManifest(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatal(err)
	}

	const limit = 1 << 20 // 1 MiB: well under any API or etcd request limit for a Secret holding the config
	if len(out) > limit {
		t.Errorf("rendered manifest is %d bytes, want <= %d", len(out), limit)
	}
}

func TestEmbeddedChartMatchesPinnedVersion(t *testing.T) {
	ch, err := loader.LoadArchive(bytes.NewReader(chartArchive))
	if err != nil {
		t.Fatal(err)
	}
	if ch.Metadata.Name != ChartName || ch.Metadata.Version != ChartVersion {
		t.Errorf("embedded chart is %s %s, want %s %s", ch.Metadata.Name, ch.Metadata.Version, ChartName, ChartVersion)
	}

	if _, err := os.Stat("chart/cilium-" + ChartVersion + ".tgz"); err != nil {
		t.Errorf("chart archive name does not follow ChartVersion: %v", err)
	}
}
