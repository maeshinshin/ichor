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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

const (
	modeKubernetes = "kubernetes"
	tunnelVxlan    = "vxlan"
	recordV1       = "sh.helm.release.v1.cilium.v1"
	ichorNS        = "ichor-system"
	kindConfigMap  = "ConfigMap"
	cfgMapName     = "cilium-config"
	valLocalhost   = "localhost"
	keyOperator    = "operator"
	ownerHelm      = "helm"
	kubeSystem     = "kube-system"
)

// storeRecord writes rel the way Helm's own Secrets driver does, so the tests
// decode real Helm output, not an encoding written from the same reading as the decoder.
func storeRecord(t *testing.T, kube *fake.Clientset, ns string, rel *release.Release) {
	t.Helper()

	s := driver.NewSecrets(kube.CoreV1().Secrets(ns))
	if err := s.Create("sh.helm.release.v1."+rel.Name+".v"+strconv.Itoa(rel.Version), rel); err != nil {
		t.Fatal(err)
	}
}

func foreignRelease(
	t *testing.T, version int, status release.Status, appVersion string, cfg map[string]any,
) *release.Release {
	t.Helper()

	ch, err := freshChart()
	if err != nil {
		t.Fatal(err)
	}

	ch.Metadata.AppVersion = appVersion

	return &release.Release{
		Name: ChartName, Namespace: kubeSystem, Version: version, Config: cfg, Chart: ch,
		Info: &release.Info{Status: status, FirstDeployed: helmtime.Unix(1, 0)},
	}
}

func TestDecodeReleaseRecord_HelmCompressedRecord(t *testing.T) {
	kube := fake.NewSimpleClientset()
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, "1.17.5",
		map[string]any{"debug": map[string]any{"enabled": true}}))

	s, err := kube.CoreV1().Secrets(kubeSystem).Get(t.Context(), "sh.helm.release.v1.cilium.v1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rel, err := DecodeReleaseRecord(s.Data["release"])
	if err != nil {
		t.Fatal(err)
	}

	if rel.Version != 1 || rel.Chart.Metadata.AppVersion != "1.17.5" || rel.Config["debug"] == nil {
		t.Errorf("decoded %d %s %v", rel.Version, rel.Chart.Metadata.AppVersion, rel.Config)
	}
}

func TestDecodeReleaseRecord_UncompressedAndBroken(t *testing.T) {
	raw, _ := json.Marshal(release.Release{Name: "cilium", Version: 3})

	rel, err := DecodeReleaseRecord([]byte(base64.StdEncoding.EncodeToString(raw)))
	if err != nil || rel.Version != 3 {
		t.Fatalf("old uncompressed record: %v, %v", rel, err)
	}

	for name, in := range map[string]string{
		"not base64": "!!!",
		"not json":   base64.StdEncoding.EncodeToString([]byte("hello")),
		"bad gzip":   base64.StdEncoding.EncodeToString([]byte{0x1f, 0x8b, 0x08, 0x00}),
	} {
		if _, err := DecodeReleaseRecord([]byte(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestForeignRecords(t *testing.T) {
	ctx := t.Context()
	kube := fake.NewSimpleClientset()

	f, err := ForeignRecords(ctx, kube, ForeignOptions{IchorNamespace: ichorNS})
	if f != nil || err != nil {
		t.Fatalf("empty cluster: %v, %v; want nil, nil", f, err)
	}

	storeRecord(t, kube, kubeSystem, foreignRelease(t, 2, release.StatusDeployed, "1.17.5", nil))
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusSuperseded, "1.17.4", nil))
	// Ichor's own record, and another release, are not foreign.
	storeRecord(t, kube, ichorNS, foreignRelease(t, 1, release.StatusDeployed, "1.18.0", nil))

	other := foreignRelease(t, 1, release.StatusDeployed, "1.0", nil)
	other.Name = "other"
	storeRecord(t, kube, kubeSystem, other)

	f, err = ForeignRecords(ctx, kube, ForeignOptions{IchorNamespace: ichorNS})
	if err != nil {
		t.Fatal(err)
	}

	if len(f.Records) != 2 || f.Records[0].Release.Version != 1 || f.Latest().Release.Version != 2 ||
		f.Namespace != kubeSystem {
		t.Fatalf("records: %d, latest %d", len(f.Records), f.Latest().Release.Version)
	}

	if err := f.RequireDeployed(); err != nil {
		t.Error(err)
	}

	// A second namespace is ambiguous.
	storeRecord(t, kube, "default", foreignRelease(t, 1, release.StatusDeployed, "1.17.5", nil))

	if _, err := ForeignRecords(ctx, kube, ForeignOptions{IchorNamespace: ichorNS}); err == nil {
		t.Error("records in two namespaces: want an error")
	}
}

func TestRequireDeployed_RefusesFailedRelease(t *testing.T) {
	kube := fake.NewSimpleClientset()
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusFailed, "1.17.5", nil))

	f, err := ForeignRecords(t.Context(), kube, ForeignOptions{IchorNamespace: ichorNS})
	if err != nil {
		t.Fatal(err)
	}

	if err := f.RequireDeployed(); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Errorf("want a refusal naming the failed status, got %v", err)
	}
}

// Precondition 1: same minor, or exactly one lower.
func TestCheckVersion(t *testing.T) { //nolint:funlen // one table
	// ChartVersion is 1.20.4, so the accepted minors are 1.20 and 1.19.
	for _, tc := range []struct {
		live    string
		ok      bool
		upgrade bool
	}{
		{"1.20.4", true, false},
		{"v1.20.7", true, false},
		{"1.19.5", true, true},
		{"quay.io/cilium/cilium:v1.19.5@sha256:abcdef", true, true},
		{"1.18.0", false, false},
		{"1.21.0", false, false},
		{"2.20.0", false, false},
		{"0.20.0", false, false},
	} {
		got, err := CheckVersion(tc.live)
		if (err == nil) != tc.ok {
			t.Errorf("CheckVersion(%q): err = %v, want ok=%t", tc.live, err, tc.ok)

			continue
		}

		if tc.ok && got.Upgrade != tc.upgrade {
			t.Errorf("CheckVersion(%q).Upgrade = %t, want %t", tc.live, got.Upgrade, tc.upgrade)
		}
	}

	if _, err := CheckVersion("latest"); err == nil {
		t.Error("an unreadable version must be refused")
	}

	if v, err := parseVersion("quay.io/cilium/cilium:v1.17.5@sha256:abc"); err != nil || v != "v1.17.5" {
		t.Errorf("parseVersion = %q, %v", v, err)
	}
}

// A cluster-pool Cilium is the chart default, so it only shows once the record's
// user values are merged with the chart defaults stored in the record.
func TestOwnedKeyConflicts_ClusterPoolIsBlockedAndNeverAccepted(t *testing.T) {
	live, err := LiveValues(foreignRelease(t, 1, release.StatusDeployed, "1.17.5", nil))
	if err != nil {
		t.Fatal(err)
	}

	cs, err := OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	c := findConflict(cs, "ipam.mode")
	if c == nil || c.Level != Blocking || c.Live != "cluster-pool" {
		t.Fatalf("ipam.mode conflict = %+v in %v", c, cs)
	}

	if err := AcceptConflicts(cs, true); err == nil || !strings.Contains(err.Error(), "ipam.mode") {
		t.Errorf("accepting must not clear a blocking conflict, got %v", err)
	}
}

func findConflict(cs []Conflict, key string) *Conflict {
	for i := range cs {
		if cs[i].Key == key {
			return &cs[i]
		}
	}

	return nil
}

func TestOwnedKeyConflicts_MatchingAndSpellings(t *testing.T) {
	user := map[string]any{
		keyIPAM:                map[string]any{keyMode: modeKubernetes},
		"kubeProxyReplacement": "true",
		"k8sServiceHost":       valLocalhost,
		"k8sServicePort":       "7445", // a string, as a CLI --set would store it
	}

	live, err := LiveValuesFromUser(user)
	if err != nil {
		t.Fatal(err)
	}

	// The same cluster as Ichor's replaced mode. Capability and cgroup keys
	// still differ from the chart default and are only informational.
	cs, err := OwnedKeyConflicts(live, cni.KubeProxyReplaced, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range cs {
		if c.Level != Informational {
			t.Errorf("unexpected conflict %v", c)
		}
	}

	if err := AcceptConflicts(cs, false); err != nil {
		t.Errorf("informational conflicts must not need acceptance: %v", err)
	}
}

// With kube-proxy enabled Ichor sets no KubePrism address (ADR 0159), so a live one has no Ichor value to
// conflict with. The takeover still removes it, which TestDiffLive_EnabledTakeoverRemovesKubePrismEnv pins.
func TestOwnedKeyConflicts_EnabledModeKubePrismHasNoIchorValue(t *testing.T) {
	live, err := LiveValuesFromUser(map[string]any{keyServiceHost: "10.0.0.1", keyServicePort: "6443"})
	if err != nil {
		t.Fatal(err)
	}

	cs, err := OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{keyServiceHost, keyServicePort} {
		if c := findConflict(cs, key); c != nil {
			t.Errorf("enabled mode: %v, want no conflict (Ichor sets no value)", c)
		}
	}

	// Replaced mode sets localhost:7445, so the same live values still conflict.
	cs, err = OwnedKeyConflicts(live, cni.KubeProxyReplaced, "")
	if err != nil {
		t.Fatal(err)
	}

	if c := findConflict(cs, keyServiceHost); c == nil || c.Level != NeedsAccept {
		t.Errorf("replaced mode k8sServiceHost = %+v, want a NeedsAccept conflict", c)
	}
}

// A live KubePrism address that enabled-mode Ichor does not set is removed by the takeover, so the diff
// gate must report it even though the owned-key report does not.
func TestDiffLive_EnabledTakeoverRemovesKubePrismEnv(t *testing.T) {
	replaced, err := EffectiveValues(nil, cni.KubeProxyReplaced, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	diffs, err := DiffLive(t.Context(), effective(t, nil), newLiveCluster(t, replaced).get)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range diffs {
		if d.Object.Kind == "DaemonSet" && d.Object.Name == ReleaseName &&
			strings.Contains(d.Path, "env["+envKubernetesHost+"]") {
			return
		}
	}

	t.Errorf("no diff for the agent's KUBERNETES_SERVICE_HOST env in %d differences", len(diffs))
}

func TestOwnedKeyConflicts_NeedsAcceptAndTunnel(t *testing.T) {
	live, err := LiveValuesFromUser(map[string]any{"ipam": map[string]any{keyMode: modeKubernetes}})
	if err != nil {
		t.Fatal(err)
	}

	// Live runs kube-proxy; spec asks for replaced: Ichor would change the data plane.
	cs, _ := OwnedKeyConflicts(live, cni.KubeProxyReplaced, "")

	if c := findConflict(cs, "kubeProxyReplacement"); c == nil || c.Level != NeedsAccept {
		t.Fatalf("kubeProxyReplacement = %+v", c)
	}

	if err := AcceptConflicts(cs, false); err == nil || !strings.Contains(err.Error(), "--accept-cilium-changes") {
		t.Errorf("without acceptance want a refusal naming the flag, got %v", err)
	}

	if err := AcceptConflicts(cs, true); err != nil {
		t.Errorf("with acceptance: %v", err)
	}

	// The tunnel protocol never changes in place; unset on both sides is vxlan.
	cs, _ = OwnedKeyConflicts(live, cni.KubeProxyEnabled, tunnelVxlan)
	if findConflict(cs, "tunnelProtocol") != nil {
		t.Error("unset tunnelProtocol equals vxlan")
	}

	cs, _ = OwnedKeyConflicts(live, cni.KubeProxyEnabled, "geneve")
	if c := findConflict(cs, "tunnelProtocol"); c == nil || c.Level != Blocking {
		t.Errorf("tunnelProtocol vxlan->geneve = %+v", c)
	}

	live["routingMode"] = "native"
	cs, _ = OwnedKeyConflicts(live, cni.KubeProxyEnabled, "")

	if c := findConflict(cs, "routingMode"); c == nil || c.Level != Blocking {
		t.Errorf("native routing = %+v", c)
	}
}

func TestUserValues_DropsOwnedKeys(t *testing.T) {
	in := map[string]any{
		"debug":           map[string]any{"enabled": true},
		keyIPAM:           map[string]any{keyMode: modeKubernetes, keyOperator: map[string]any{"x": 1}},
		keyTunnelProtocol: tunnelVxlan,
	}

	out := UserValues(in)

	if _, ok := out["tunnelProtocol"]; ok || out["debug"] == nil {
		t.Errorf("out = %v", out)
	}

	if ipam, _ := out["ipam"].(map[string]any); ipam["mode"] != nil || ipam["operator"] == nil {
		t.Errorf("ipam = %v", out["ipam"])
	}

	if in["tunnelProtocol"] != tunnelVxlan {
		t.Error("the input was modified")
	}
}

// --- diff gate ------------------------------------------------------------

// liveCluster serves the chart's own rendering back as the "live" objects, with
// the volatile fields a real server adds.
type liveCluster map[ObjectRef]map[string]any

func (c liveCluster) get(_ context.Context, ref ObjectRef) (map[string]any, error) {
	o, ok := c[ref]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: strings.ToLower(ref.Kind)}, ref.Name)
	}

	return values.Clone(o), nil
}

func newLiveCluster(t *testing.T, vals map[string]any) liveCluster {
	t.Helper()

	rendered, err := renderChart(vals)
	if err != nil {
		t.Fatal(err)
	}

	objs, err := splitObjects(rendered)
	if err != nil {
		t.Fatal(err)
	}

	c := liveCluster{}

	for _, o := range objs {
		ref := refOf(o)
		meta := o["metadata"].(map[string]any)
		if ref.Namespace != "" {
			meta["namespace"] = ref.Namespace
		}
		meta["uid"] = "u-" + ref.Name
		meta["resourceVersion"] = "42"
		meta["creationTimestamp"] = "2026-01-01T00:00:00Z"
		meta["managedFields"] = []any{map[string]any{"manager": "kubectl"}}
		o["status"] = map[string]any{"observedGeneration": 1}

		if ref.Kind == "ClusterRole" || ref.Kind == "ClusterRoleBinding" {
			delete(meta, "namespace")
		}

		c[ref] = o
	}

	return c
}

func mergeInto(meta map[string]any, field, k string, v any) {
	m, _ := meta[field].(map[string]any)
	if m == nil {
		m = map[string]any{}
		meta[field] = m
	}

	m[k] = v
}

func (c liveCluster) agent(t *testing.T) map[string]any {
	t.Helper()

	for ref, o := range c {
		if ref.Kind == kindDaemonSet && ref.Name == "cilium" {
			return o
		}
	}

	t.Fatal("no cilium DaemonSet rendered")

	return nil
}

func agentContainer(t *testing.T, ds map[string]any) map[string]any {
	t.Helper()

	pod := ds["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	for _, c := range pod["containers"].([]any) {
		if cm := c.(map[string]any); cm["name"] == "cilium-agent" {
			return cm
		}
	}

	t.Fatal("no cilium-agent container")

	return nil
}

func diffLive(t *testing.T, vals map[string]any, live liveCluster) []Difference {
	t.Helper()

	got, err := DiffLive(t.Context(), vals, live.get)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func diffPaths(ds []Difference) []string {
	out := make([]string, 0, len(ds))

	for _, d := range ds {
		out = append(out, string(d.Kind)+" "+d.Object.Kind+"/"+d.Object.Name+" "+d.Path)
	}

	return out
}

func TestDiffLive_IdenticalClusterIsEmpty(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	// Helm metadata of another release, or none at all, is not a difference.
	for ref, o := range live {
		meta := o["metadata"].(map[string]any)
		mergeInto(meta, "labels", "app.kubernetes.io/managed-by", "Talos")
		mergeInto(meta, "annotations", "kubectl.kubernetes.io/last-applied-configuration", "{}")

		if ref.Kind == kindConfigMap || ref.Kind == "Service" {
			mergeInto(meta, "annotations", "meta.helm.sh/release-name", "mine")
		}
	}

	got := diffLive(t, vals, live)

	if len(got) != 0 {
		t.Fatalf("want no difference, got:\n%s", strings.Join(diffPaths(got), "\n"))
	}
}

// An extra annotation and an extra argument on the live
// DaemonSet are reported; an extra object is not (takeover keeps it).
func TestDiffLive_ReportsLiveOnlyAnnotationAndArg(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	ds := live.agent(t)
	mergeInto(ds["metadata"].(map[string]any), "annotations", "example.com/owner", "someone")

	ctr := agentContainer(t, ds)
	ctr["args"] = append(ctr["args"].([]any), "--debug")

	extra := ObjectRef{APIVersion: "v1", Kind: kindConfigMap, Namespace: kubeSystem, Name: "extra"}
	live[extra] = map[string]any{"kind": kindConfigMap}

	got := diffLive(t, vals, live)

	paths := strings.Join(diffPaths(got), "\n")
	if len(got) != 2 ||
		!strings.Contains(paths, "LiveOnly DaemonSet/cilium metadata.annotations.example.com/owner") ||
		!strings.Contains(paths, "LiveOnly DaemonSet/cilium spec.template.spec.containers[cilium-agent].args") {
		t.Fatalf("differences:\n%s", paths)
	}
}

func TestDiffLive_IgnoresTalosOwningInventoryAnnotation(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	meta := live.agent(t)["metadata"].(map[string]any)
	mergeInto(meta, "annotations", "config.k8s.io/owning-inventory", "abc123")
	mergeInto(meta, "annotations", "config.k8s.io/owning-inventory-other", "x")

	got := diffLive(t, vals, live)

	paths := strings.Join(diffPaths(got), "\n")
	if len(got) != 1 || !strings.Contains(paths, "metadata.annotations.config.k8s.io/owning-inventory-other") {
		t.Fatalf("want only the near-miss key reported, got:\n%s", paths)
	}
}

func TestDiffLive_ChangedFieldAndMissingObject(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	agentContainer(t, live.agent(t))["image"] = "quay.io/cilium/cilium:v1.17.5"

	var gone ObjectRef

	for ref := range live {
		if ref.Kind == kindConfigMap && ref.Name == cfgMapName {
			gone = ref
		}
	}

	delete(live, gone)

	got := diffLive(t, vals, live)

	var changed, missing bool

	for _, d := range got {
		changed = changed || (d.Kind == DiffChanged && d.Path == "spec.template.spec.containers[cilium-agent].image")
		missing = missing || (d.Kind == DiffObjectMissing && d.Object.Name == "cilium-config")
	}

	if !changed || !missing {
		t.Fatalf("changed=%t missing=%t in:\n%s", changed, missing, strings.Join(diffPaths(got), "\n"))
	}
}

func TestDiffLive_SecretDataIsNeverCompared(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	for ref, o := range live {
		if ref.Kind == "Secret" {
			o["data"] = map[string]any{"tls.key": "TOP-SECRET"}
		}
	}

	got := diffLive(t, vals, live)

	for _, d := range got {
		if strings.Contains(d.String(), "TOP-SECRET") {
			t.Errorf("a Secret value leaked into %s", d)
		}
	}
}

func TestDiffLive_ListHelpers(t *testing.T) {
	ref := ObjectRef{Kind: "X", Name: "x"}

	// Same elements in another order is a difference.
	got := diffScalars(ref, "args", []any{"a", "b"}, []any{"b", "a"})
	if len(got) != 1 || got[0].Kind != DiffChanged {
		t.Errorf("reordered args = %v", got)
	}

	// An extra and a missing element are each one difference.
	got = diffScalars(ref, "args", []any{"a", "b"}, []any{"a", "c"})
	if len(got) != 2 {
		t.Errorf("a/b vs a/c = %v", got)
	}

	// A number is not a string.
	if sameScalar(7445, "7445") || !sameScalar(7445.0, 7445) {
		t.Error("sameScalar")
	}
}

// --- crds/ -----------------------------------------------------------------

func TestChartCRDs(t *testing.T) {
	if err := CheckChartCRDs(); err != nil {
		t.Fatalf("the pinned chart must ship no crds/: %v", err)
	}

	ch := &chart.Chart{Metadata: &chart.Metadata{Name: "x", Version: "1"}, Files: []*chart.File{
		{Name: "crds/a.yaml", Data: []byte("kind: CustomResourceDefinition")},
		{Name: "README.md"},
	}}

	if got := crdNames(ch); len(got) != 1 || got[0] != "crds/a.yaml" {
		t.Errorf("crdNames = %v, want [crds/a.yaml]", got)
	}

	if got := crdNames(&chart.Chart{Metadata: &chart.Metadata{Name: "x"}}); len(got) != 0 {
		t.Errorf("crdNames of a chart without crds/ = %v", got)
	}
}

// --- foreign record backup and removal -----------------------------------

func objectsOnly(t *testing.T, kube *fake.Clientset) {
	t.Helper()

	for _, a := range kube.Actions() {
		if a.GetResource().Resource != "secrets" {
			t.Errorf("touched %s (%s): only Secrets may be touched, never the release's objects",
				a.GetResource().Resource, a.GetVerb())
		}
	}
}

func TestRetireForeignRecord_BacksUpThenDeletesOnlyTheRecords(t *testing.T) {
	ctx := t.Context()
	kube := fake.NewSimpleClientset(
		&corev1.Secret{Name: "unrelated", Namespace: kubeSystem},
	)

	rev1 := foreignRelease(t, 1, release.StatusSuperseded, "1.17.4", nil)
	rev2 := foreignRelease(t, 2, release.StatusDeployed, "1.17.5", nil)
	storeRecord(t, kube, kubeSystem, rev1)
	storeRecord(t, kube, kubeSystem, rev2)
	storeRecord(t, kube, ichorNS, foreignRelease(t, 1, release.StatusDeployed, "1.18.0", nil))

	before, _ := kube.CoreV1().Secrets(kubeSystem).Get(ctx, "sh.helm.release.v1.cilium.v2", metav1.GetOptions{})

	res, err := RetireForeignRecord(ctx, kube, RetireOptions{IchorNamespace: ichorNS, RemoveForeignReleaseRecord: true})
	if err != nil {
		t.Fatal(err)
	}

	if res.Kept || len(res.Removed) != 2 || res.Backup != PriorReleaseBackupName {
		t.Fatalf("result = %+v", res)
	}

	// The foreign records are gone ...
	list, _ := kube.CoreV1().Secrets(kubeSystem).List(ctx, metav1.ListOptions{})
	if len(list.Items) != 1 || list.Items[0].Name != "unrelated" {
		t.Errorf("kube-system Secrets after = %d", len(list.Items))
	}

	// ... Ichor's own record is not ...
	if _, err := kube.CoreV1().Secrets(ichorNS).Get(ctx, "sh.helm.release.v1.cilium.v1", metav1.GetOptions{}); err != nil {
		t.Errorf("Ichor's own record: %v", err)
	}

	// ... and the backup restores them: bytes and metadata.
	backup, err := kube.CoreV1().Secrets(ichorNS).Get(ctx, PriorReleaseBackupName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(backup.Data["kube-system.sh.helm.release.v1.cilium.v2.release"], before.Data["release"]) {
		t.Error("the backup does not hold the original record bytes")
	}

	var meta recordMeta
	if err := json.Unmarshal(backup.Data["kube-system.sh.helm.release.v1.cilium.v2.meta"], &meta); err != nil {
		t.Fatal(err)
	}

	if meta.Name != "sh.helm.release.v1.cilium.v2" || meta.Namespace != kubeSystem ||
		meta.Type != "helm.sh/release.v1" || meta.Labels["status"] != "deployed" {
		t.Errorf("backup metadata = %+v", meta)
	}

	objectsOnly(t, kube)

	// A second run finds nothing, and does not touch the backup.
	res, err = RetireForeignRecord(ctx, kube, RetireOptions{IchorNamespace: ichorNS, RemoveForeignReleaseRecord: true})
	if err != nil || !res.Kept || len(res.Removed) != 0 {
		t.Errorf("second run = %+v, %v", res, err)
	}
}

// The option is the user's pending decision: off, nothing is written at all.
func TestRetireForeignRecord_OptionOffChangesNothing(t *testing.T) {
	kube := fake.NewSimpleClientset()
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, "1.17.5", nil))
	kube.ClearActions()

	res, err := RetireForeignRecord(t.Context(), kube, RetireOptions{IchorNamespace: ichorNS})
	if err != nil || !res.Kept {
		t.Fatalf("result = %+v, %v", res, err)
	}

	if n := len(kube.Actions()); n != 0 {
		t.Errorf("made %d API calls with the option off", n)
	}
}

// A backup that cannot be made keeps every record.
func TestRetireForeignRecord_BackupFailureKeepsRecords(t *testing.T) {
	ctx := t.Context()
	kube := fake.NewSimpleClientset()
	storeRecord(t, kube, kubeSystem, foreignRelease(t, 1, release.StatusDeployed, "1.17.5", nil))

	kube.PrependReactor("create", "secrets", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == ichorNS {
			gr := schema.GroupResource{Resource: "secrets"}

			return true, nil, apierrors.NewForbidden(gr, PriorReleaseBackupName, errors.New("denied"))
		}

		return false, nil, nil
	})

	_, err := RetireForeignRecord(ctx, kube, RetireOptions{IchorNamespace: ichorNS, RemoveForeignReleaseRecord: true})
	if err == nil {
		t.Fatal("want an error")
	}

	if _, err := kube.CoreV1().Secrets(kubeSystem).Get(ctx, recordV1, metav1.GetOptions{}); err != nil {
		t.Errorf("the record was deleted although the backup failed: %v", err)
	}
}

// A backup that is too big for one Secret refuses before deleting anything.
func TestRetireForeignRecord_OversizeBackupKeepsRecords(t *testing.T) {
	ctx := t.Context()
	// The record is padded past the cap while it still decodes.
	kube := fake.NewSimpleClientset(&corev1.Secret{
		Name: recordV1, Namespace: kubeSystem,
		Labels: map[string]string{"owner": ownerHelm, "name": ChartName},
		Type:   helmReleaseSecretType,
		Data: map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString([]byte(
			`{"version":1,"config":{"p":"` + strings.Repeat("x", maxBackupBytes) + `"}}`)))},
	})

	_, err := RetireForeignRecord(ctx, kube, RetireOptions{IchorNamespace: ichorNS, RemoveForeignReleaseRecord: true})
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("want a size refusal, got %v", err)
	}

	if _, err := kube.CoreV1().Secrets(kubeSystem).Get(ctx, recordV1, metav1.GetOptions{}); err != nil {
		t.Errorf("record deleted: %v", err)
	}
}

func TestDiffLive_IgnoresServerDefaultedRollingUpdateAndServiceType(t *testing.T) {
	vals := effective(t, nil)
	live := newLiveCluster(t, vals)

	for ref, o := range live {
		spec, _ := o["spec"].(map[string]any)

		switch ref.Kind {
		case "DaemonSet":
			spec["updateStrategy"].(map[string]any)["rollingUpdate"].(map[string]any)["maxSurge"] = int64(0)
		case "Service":
			if ref.Name == "hubble-peer" {
				spec["type"] = "ClusterIP"
			}
		}
	}

	got := diffLive(t, vals, live)

	if len(got) != 0 {
		t.Fatalf("server defaults reported:\n%s", strings.Join(diffPaths(got), "\n"))
	}

	// Only the exact default is ignored: a surge the user set is still a difference.
	spec := live.agent(t)["spec"].(map[string]any)
	spec["updateStrategy"].(map[string]any)["rollingUpdate"].(map[string]any)["maxSurge"] = int64(1)

	got = diffLive(t, vals, live)
	if len(got) != 1 || got[0].Path != "spec.updateStrategy.rollingUpdate.maxSurge" {
		t.Fatalf("got %v", diffPaths(got))
	}
}

func TestDifferenceStringTruncatesLargeValues(t *testing.T) {
	big := strings.Repeat("x", 8000)
	got := Difference{Path: "data.k", Kind: DiffChanged, Rendered: big, Live: "small"}.String()

	if len(got) > 600 || !strings.Contains(got, "… (8000 bytes)") || !strings.Contains(got, "live small") {
		t.Errorf("String() = %q", got)
	}
}
