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

package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// These run against a real kube-apiserver (envtest): server-side apply, the CRD schema
// handling and the restore-by-update are the behaviour under test, which a fake client
// would only pretend to have.

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	testCfg   *rest.Config
)

func TestMain(m *testing.M) {
	testEnv = &envtest.Environment{}

	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting envtest:", err)
		os.Exit(1)
	}

	testCfg = cfg

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		fmt.Fprintln(os.Stderr, "building scheme:", err)
		os.Exit(1)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintln(os.Stderr, "building client:", err)
		os.Exit(1)
	}

	code := m.Run()

	if err := testEnv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "stopping envtest:", err)
	}

	os.Exit(code)
}

// fixture is what one test installs. Every name is unique to the test (CRDs are
// cluster-scoped and the API server is shared), so tests do not clean up after each other.
type fixture struct {
	ns, plural, group, run string
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	ns := &corev1.Namespace{GenerateName: "installer-test-"}
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatalf("creating namespace: %v", err)
	}

	id := strings.TrimPrefix(ns.Name, "installer-test-")

	return fixture{ns: ns.Name, plural: "widgets", group: id + ".installer.test", run: "run-" + id}
}

// manifest renders the fixture's install manifest: a CRD (with extraField when non-empty),
// a controller Deployment and a UI Deployment, in the order kustomize emits them. A Secret
// rides along to prove it is never touched.
func (f fixture) manifest(image, extraField string) []byte {
	extra := ""
	if extraField != "" {
		extra = "\n              " + extraField + ":\n                type: string"
	}

	dep := func(name string) string {
		return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    matchLabels: {app: %s}
  template:
    metadata:
      labels: {app: %s}
    spec:
      containers:
      - name: m
        image: %s
`, name, f.ns, name, name, image)
	}

	return []byte(fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %s.%s
spec:
  group: %s
  names: {kind: Widget, listKind: WidgetList, plural: %s, singular: widget}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              size:
                type: string%s
%s%s---
apiVersion: v1
kind: Secret
metadata:
  name: cert
  namespace: %s
stringData: {k: from-manifest}
`, f.plural, f.group, f.group, f.plural, extra, dep("ichor-controller-manager"), dep("ichor-ui"), f.ns))
}

func (f fixture) image(t *testing.T, name string) string {
	t.Helper()

	var d unstructured.Unstructured
	d.SetAPIVersion("apps/v1")
	d.SetKind("Deployment")

	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: f.ns, Name: name}, &d); err != nil {
		if apierrors.IsNotFound(err) {
			return ""
		}

		t.Fatalf("getting %s: %v", name, err)
	}

	containers, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")

	return containers[0].(map[string]any)["image"].(string)
}

func (f fixture) crdHasField(t *testing.T, field string) bool {
	t.Helper()

	var crd unstructured.Unstructured
	crd.SetAPIVersion("apiextensions.k8s.io/v1")
	crd.SetKind(kindCRD)

	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: f.plural + "." + f.group}, &crd); err != nil {
		if apierrors.IsNotFound(err) {
			return false
		}

		t.Fatalf("getting the CRD: %v", err)
	}

	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	_, found, _ := unstructured.NestedMap(versions[0].(map[string]any), "schema", "openAPIV3Schema", "properties", "spec", "properties", field)

	return found
}

// noWait skips the post-check (envtest has no Deployment controller), recording the order.
func noWait(order *[]string) ReadyFunc {
	return func(_ context.Context, _ client.Client, obj *unstructured.Unstructured) error {
		*order = append(*order, obj.GetKind()+"/"+obj.GetName())

		return nil
	}
}

// failOn fails the post-check of the object named name with msg.
func failOn(name, msg string) ReadyFunc {
	return func(_ context.Context, _ client.Client, obj *unstructured.Unstructured) error {
		if obj.GetName() == name {
			return errors.New(msg)
		}

		return nil
	}
}

func TestRunAppliesCRDsThenControllerThenUI(t *testing.T) {
	f := newFixture(t)

	var order []string

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: f.manifest("ctrl:v2", "color"), Ready: noWait(&order),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"CustomResourceDefinition/" + f.plural + "." + f.group, "Deployment/ichor-controller-manager", "Deployment/ichor-ui"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("post-check order = %v, want %v", order, want)
	}

	if got := f.image(t, "ichor-controller-manager"); got != "ctrl:v2" {
		t.Errorf("controller image = %q, want ctrl:v2", got)
	}

	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: f.ns, Name: "cert"}, &corev1.Secret{}); !apierrors.IsNotFound(err) {
		t.Errorf("the Secret in the manifest must not be applied, Get error = %v", err)
	}
}

// A status write by the Deployment controller between applyOne's Get and Update surfaces as a
// Conflict; the pair must be retried rather than fail the step.
func TestApplyOneRetriesADeploymentUpdateConflict(t *testing.T) {
	deployment := func(image string) *unstructured.Unstructured {
		d := &appsv1.Deployment{
			APIVersion: "apps/v1", Kind: "Deployment",
			Name: "d", Namespace: "ns",
		}
		d.Spec.Template.Spec.Containers = []corev1.Container{{Name: "m", Image: image}}

		obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(d)
		if err != nil {
			t.Fatalf("converting: %v", err)
		}

		return &unstructured.Unstructured{Object: obj}
	}

	updates := 0
	c := fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).WithObjects(deployment("old")).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				updates++
				if updates == 1 {
					return apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "d", errors.New("the object has been modified"))
				}

				return cl.Update(ctx, obj, opts...)
			},
		}).Build()

	if err := applyOne(context.Background(), c, deployment("new")); err != nil {
		t.Fatalf("applyOne: %v", err)
	}

	if updates != 2 {
		t.Errorf("Update calls = %d, want 2", updates)
	}

	got := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "d"}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if image := got.Spec.Template.Spec.Containers[0].Image; image != "new" {
		t.Errorf("image = %q, want new", image)
	}
}

func TestRunRefusesACRDThatWouldDropAField(t *testing.T) {
	f := newFixture(t)

	// The cluster already runs the newer CRD (with "color")...
	runInstaller(t, f, f.run+"-new", f.manifest("ctrl:v2", "color"))

	// ...and an installer carrying the older manifest (no "color") must be refused before any write.
	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run + "-old", Manifest: f.manifest("ctrl:v1", ""), Ready: noWait(new([]string)),
	})
	if err == nil || !strings.Contains(err.Error(), "color would be removed") {
		t.Fatalf("Run error = %v, want a refusal naming the dropped field", err)
	}

	if !f.crdHasField(t, "color") {
		t.Error("the live CRD lost its field")
	}

	if got := f.image(t, "ichor-controller-manager"); got != "ctrl:v2" {
		t.Errorf("controller image = %q: a refused upgrade must not write", got)
	}

	if l, _ := loadSnapshot(context.Background(), k8sClient, f.ns, f.run+"-old"); l != nil {
		t.Error("a refused upgrade must not leave a snapshot")
	}
}

func TestRunRollsBackWhenAPostCheckFails(t *testing.T) {
	f := newFixture(t)

	// Version 1 is installed.
	runInstaller(t, f, f.run+"-v1", f.manifest("ctrl:v1", ""))

	// Remove the UI so the failed upgrade has something it created to delete again.
	ui := &unstructured.Unstructured{}
	ui.SetAPIVersion("apps/v1")
	ui.SetKind("Deployment")
	ui.SetNamespace(f.ns)
	ui.SetName("ichor-ui")

	if err := k8sClient.Delete(context.Background(), ui); err != nil {
		t.Fatalf("deleting the UI: %v", err)
	}

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run + "-v2", Manifest: f.manifest("ctrl:v2", "color"), Ready: failOn("ichor-ui", "UI never became ready"),
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") || !strings.Contains(err.Error(), "UI never became ready") {
		t.Fatalf("Run error = %v, want the cause and a rollback", err)
	}

	if got := f.image(t, "ichor-controller-manager"); got != "ctrl:v1" {
		t.Errorf("controller image = %q, want it restored to ctrl:v1", got)
	}

	if got := f.image(t, "ichor-ui"); got != "" {
		t.Errorf("the UI the upgrade created must be deleted again, found image %q", got)
	}

	// The CRD is kept: deleting a CRD deletes its custom resources, and the added field is harmless.
	if !f.crdHasField(t, "size") {
		t.Error("the CRD must survive a rollback")
	}

	if l, err := loadSnapshot(context.Background(), k8sClient, f.ns, f.run+"-v2"); err != nil || l == nil {
		t.Errorf("the snapshot must stay as evidence, got %v, %v", l, err)
	}
}

func TestSnapshotIsKeptWhenARunIsRepeated(t *testing.T) {
	f := newFixture(t)

	// A first run that fails after the snapshot leaves the original in place...
	_ = Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: f.manifest("ctrl:v2", ""), Ready: failOn("ichor-controller-manager", "boom"),
	})

	first, err := loadSnapshot(context.Background(), k8sClient, f.ns, f.run)
	if err != nil || len(first) == 0 {
		t.Fatalf("snapshot after the first run: %v, %v", first, err)
	}

	// ...and a second run with the same id reuses it rather than snapshotting the mid-upgrade state.
	runInstaller(t, f, f.run, f.manifest("ctrl:v2", ""))

	second, _ := loadSnapshot(context.Background(), k8sClient, f.ns, f.run)
	if len(second) != len(first) {
		t.Errorf("snapshot changed between runs: %d entries, then %d", len(first), len(second))
	}

	for i, e := range second {
		if (e.Object == nil) != (first[i].Object == nil) {
			t.Errorf("entry %s %s changed from absent/present between runs", e.Kind, e.Name)
		}
	}
}

func snapshotRuns(t *testing.T, ns string) map[string]int {
	t.Helper()

	var l corev1.ConfigMapList
	if err := k8sClient.List(context.Background(), &l, client.InNamespace(ns), client.HasLabels{SnapshotLabel}); err != nil {
		t.Fatalf("listing snapshots: %v", err)
	}

	out := map[string]int{}
	for _, cm := range l.Items {
		out[cm.Labels[SnapshotLabel]]++
	}

	return out
}

func TestRunKeepsOnlyTheNewestSnapshots(t *testing.T) {
	f := newFixture(t)

	for i := range SnapshotsKept + 2 {
		runInstaller(t, f, fmt.Sprintf("%s-%d", f.run, i), f.manifest("ctrl:v2", ""))
	}

	got := snapshotRuns(t, f.ns)
	if len(got) != SnapshotsKept {
		t.Fatalf("snapshots after %d runs = %v, want the newest %d", SnapshotsKept+2, got, SnapshotsKept)
	}

	for i := 2; i < SnapshotsKept+2; i++ {
		if got[fmt.Sprintf("%s-%d", f.run, i)] == 0 {
			t.Errorf("run %d's snapshot was deleted: %v", i, got)
		}
	}
}

func TestPruneNeverDeletesTheCurrentSnapshot(t *testing.T) {
	f := newFixture(t)

	for _, id := range []string{"b", "a", "c"} {
		if _, err := takeSnapshot(context.Background(), k8sClient, f.ns, f.run+id, 0, nil); err != nil {
			t.Fatal(err)
		}
	}

	// keep 1 would keep only "c" (newest); "b" is current and the oldest, so it stays too.
	if err := pruneSnapshots(context.Background(), k8sClient, f.ns, f.run+"b", 1); err != nil {
		t.Fatal(err)
	}

	got := snapshotRuns(t, f.ns)
	if len(got) != 2 || got[f.run+"b"] == 0 || got[f.run+"c"] == 0 {
		t.Errorf("snapshots = %v, want b (current) and c (newest)", got)
	}
}

// Kustomize prefixes every name with ichor-, so the UI objects are ichor-ichor-ui-*; the
// installer's own objects must land with the controller, not the UI.
// TestRunAppliesItsOwnRBACFirst pins: the installer's roles and bindings
// are applied (and snapshotted) before the CRDs, so an upgrade started by an older release
// grants itself the rights the new manifest needs.
func TestRunAppliesItsOwnRBACFirst(t *testing.T) {
	f := newFixture(t)

	rbac := fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: ichor-installer-role, namespace: %[1]s}
rules:
- apiGroups: [""]
  resources: [configmaps]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ichor-installer-rolebinding, namespace: %[1]s}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: ichor-installer-role}
subjects: [{kind: ServiceAccount, name: ichor-installer, namespace: %[1]s}]
`, f.ns)

	// Appended last in the manifest, as kustomize may emit it anywhere.
	manifest := append(f.manifest("ctrl:v2", ""), []byte(rbac)...)

	var order []string

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: manifest, Ready: noWait(&order),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{
		"Role/ichor-installer-role", "RoleBinding/ichor-installer-rolebinding",
		"CustomResourceDefinition/" + f.plural + "." + f.group, "Deployment/ichor-controller-manager", "Deployment/ichor-ui",
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("post-check order = %v, want %v", order, want)
	}

	snap, err := loadSnapshot(context.Background(), k8sClient, f.ns, f.run)
	if err != nil {
		t.Fatalf("loadSnapshot: %v", err)
	}

	if len(snap) == 0 || snap[0].Kind != kindRole {
		t.Errorf("the snapshot must start with the installer RBAC so a rollback restores it last, got %+v", snap)
	}
}

func TestClassifySplitsByKindThenUIName(t *testing.T) {
	mk := func(kind, name string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetKind(kind)
		u.SetName(name)

		if kind == "Role" || kind == "RoleBinding" {
			u.SetNamespace("ichor-system")
		}

		return u
	}

	p := classify([]*unstructured.Unstructured{
		mk("Namespace", "ichor-system"),
		mk(kindCRD, "talosclusters.ichor.maesh.dev"),
		mk("ClusterRole", "ichor-ichor-ui-role"),
		mk("Deployment", "ichor-ichor-ui"),
		mk("Service", "ichor-ichor-ui-service"),
		mk("ServiceAccount", "ichor-installer"),
		mk(kindSecret, "ichor-webhook-server-cert"),
		mk("Deployment", "ichor-controller-manager"),
		mk("ClusterRole", "ichor-installer-role"),
		mk("ClusterRoleBinding", "ichor-installer-rolebinding"),
		mk("Role", "ichor-installer-role"),
		mk("RoleBinding", "ichor-installer-rolebinding"),
	})

	if len(p.clusterRBAC) != 2 || len(p.namespacedRBAC) != 2 || len(p.crds) != 1 || len(p.ui) != 3 || len(p.controller) != 3 {
		t.Errorf("cluster rbac/namespaced rbac/crds/ui/controller = %d/%d/%d/%d/%d, want 2/2/1/3/3 (the Secret is in none)",
			len(p.clusterRBAC), len(p.namespacedRBAC), len(p.crds), len(p.ui), len(p.controller))
	}
}

func TestRolloutDone(t *testing.T) {
	d := func(gen, observed, replicas, updated, total, available int64) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{
			"spec":   map[string]any{"replicas": replicas},
			"status": map[string]any{"observedGeneration": observed, "updatedReplicas": updated, "replicas": total, "availableReplicas": available},
		}}
		u.SetGeneration(gen)

		return u
	}

	if !rolledOut(d(2, 2, 1, 1, 1, 1)) {
		t.Error("a finished rollout was reported as unfinished")
	}

	if rolledOut(d(3, 2, 1, 1, 1, 1)) {
		t.Error("a Deployment whose spec the controller has not seen was reported as rolled out")
	}

	if rolledOut(d(2, 2, 1, 1, 2, 1)) {
		t.Error("a rollout with an old replica still around was reported as finished")
	}
}

// The Cilium Job's Role and RoleBinding in cilium-secrets are part of
// the install manifest, but that namespace exists only where Cilium runs: on another CNI the apply
// of them is skipped, not an upgrade that fails. A missing namespace anywhere else still fails.
func TestRunSkipsObjectsInTheCiliumSecretsNamespaceWhereItDoesNotExist(t *testing.T) {
	role := func(ns string) []byte {
		return []byte(fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ichor-cni-secrets-role
  namespace: %s
rules:
- apiGroups: [""]
  resources: [secrets]
  verbs: [get]
`, ns))
	}

	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: CiliumSecretsNamespace}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("this test needs envtest without a %s namespace, Get error = %v", CiliumSecretsNamespace, err)
	}

	f := newFixture(t)

	var order []string

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: append(f.manifest("ctrl:v2", ""), role(CiliumSecretsNamespace)...), Ready: noWait(&order),
	})
	if err != nil {
		t.Fatalf("Run with a Role in a missing %s: %v", CiliumSecretsNamespace, err)
	}

	for _, o := range order {
		if strings.HasPrefix(o, "Role/") {
			t.Errorf("post-check ran for %s, which was not applied", o)
		}
	}

	f = newFixture(t)

	err = Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: append(f.manifest("ctrl:v2", ""), role("no-such-namespace")...), Ready: noWait(&order),
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Run with a Role in another missing namespace = %v, want a not found error", err)
	}
}

// A Cilium cluster whose namespace did not exist yet when the pair was first skipped gets the Role the next
// time the install manifest is applied: the skip is not remembered.
func TestRunAppliesTheCiliumSecretsRoleOnceTheNamespaceExists(t *testing.T) {
	ctx := context.Background()

	role := []byte(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ichor-cni-secrets-role
  namespace: ` + CiliumSecretsNamespace + `
rules:
- apiGroups: [""]
  resources: [secrets]
  verbs: [get]
`)

	getRole := func() error {
		live := &unstructured.Unstructured{}
		live.SetAPIVersion("rbac.authorization.k8s.io/v1")
		live.SetKind("Role")

		return k8sClient.Get(ctx, types.NamespacedName{Namespace: CiliumSecretsNamespace, Name: "ichor-cni-secrets-role"}, live)
	}

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: CiliumSecretsNamespace}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("this test needs envtest without a %s namespace, Get error = %v", CiliumSecretsNamespace, err)
	}

	run := func() {
		t.Helper()

		f := newFixture(t)

		var order []string

		if err := Run(ctx, k8sClient, Options{
			Namespace: f.ns, RunID: f.run, Manifest: append(f.manifest("ctrl:v2", ""), role...), Ready: noWait(&order),
		}); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	run()

	if err := getRole(); !apierrors.IsNotFound(err) {
		t.Fatalf("the Role before the namespace exists: Get error = %v, want not found", err)
	}

	ns := &corev1.Namespace{}
	ns.Name = CiliumSecretsNamespace

	if err := k8sClient.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}

	// envtest has no namespace controller: remove the namespace through its finalize subresource so the
	// next test finds none.
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, ns)
		ns.Spec.Finalizers = nil
		_ = k8sClient.SubResource("finalize").Update(ctx, ns)
	})

	run()

	if err := getRole(); err != nil {
		t.Fatalf("the Role after the namespace exists: %v", err)
	}
}
