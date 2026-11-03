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
	"fmt"
	"io"
	"os"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const rbacAPI = "rbac.authorization.k8s.io/v1"

// TestPruneCandidatesSelectsOnlyWhatTheBundleDrops is the kind and namespace rule: an inventory
// object is a candidate only when the bundle drops it, it is of a prunable kind, it is cluster-scoped
// or in the installer's namespace, and it is not the installer's own RBAC.
func TestPruneCandidatesSelectsOnlyWhatTheBundleDrops(t *testing.T) {
	const ns = "ichor-system"

	prev := []ref{
		{APIVersion: rbacAPI, Kind: kindClusterRole, Name: "ichor-manager-role"},
		{APIVersion: rbacAPI, Kind: kindClusterRole, Name: "ichor-kept"},
		{APIVersion: rbacAPI, Kind: kindRole, Namespace: ns, Name: "stale-role"},
		{APIVersion: rbacAPI, Kind: kindRole, Namespace: "kube-system", Name: "other-namespace"},
		{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition", Name: "talosclusters.ichor.maesh.dev"},
		{APIVersion: "v1", Kind: "Namespace", Name: "old-namespace"},
		{APIVersion: "v1", Kind: "Secret", Namespace: ns, Name: "state-secret"},
		{APIVersion: "scheduling.k8s.io/v1", Kind: "PriorityClass", Name: "ichor-high"},
		{APIVersion: "cilium.io/v2", Kind: "CiliumNetworkPolicy", Namespace: ns, Name: "ichor-old-policy"},
		{APIVersion: rbacAPI, Kind: kindClusterRole, Name: "ichor-installer-helper"},
	}
	bundle := []ref{{APIVersion: rbacAPI, Kind: kindClusterRole, Name: "ichor-kept"}}

	got := make([]string, 0, 3)
	var renamedTo string

	for _, c := range pruneCandidates(prev, bundle, ns) {
		got = append(got, c.Kind+"/"+c.Name)

		if c.Name == breakGlassRole {
			renamedTo = c.renamedTo
		}
	}

	want := []string{"ClusterRole/ichor-manager-role", "Role/stale-role", "ClusterRole/ichor-ichor-break-glass-role"}
	if !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}

	if renamedTo != overrideRole {
		t.Errorf("the renamed role says it became %q, want ichor-ichor-override-role", renamedTo)
	}
}

// A renamed object is not a candidate while the bundle still ships its old name.
func TestPruneCandidatesNeverPrunesAShippedRenamedName(t *testing.T) {
	bundle := []ref{{APIVersion: rbacAPI, Kind: kindClusterRole, Name: breakGlassRole}}

	if got := pruneCandidates(nil, bundle, "ichor-system"); len(got) != 0 {
		t.Errorf("candidates = %v, want none: the bundle still ships the name", got)
	}
}

// The mark: an object is the installer's when it carries the installed-by label; a renamed object
// from before that label also counts with the app name label; nothing else does.
func TestOwnedNeedsTheInstalledByLabelOrARenamesAppLabel(t *testing.T) {
	plain := ref{APIVersion: rbacAPI, Kind: kindClusterRole, Name: "ichor-manager-role"}.candidate("")
	renamed := ref{APIVersion: rbacAPI, Kind: kindClusterRole, Name: breakGlassRole}.candidate(overrideRole)

	withLabels := func(labels map[string]string) *unstructured.Unstructured {
		o := &unstructured.Unstructured{}
		o.SetLabels(labels)

		return o
	}

	cases := []struct {
		name   string
		live   *unstructured.Unstructured
		cand   pruneCandidate
		expect bool
	}{
		{"installed-by label", withLabels(map[string]string{InstalledByLabel: InstalledByValue}), plain, true},
		{"installed-by with another value", withLabels(map[string]string{InstalledByLabel: "someone-else"}), plain, false},
		{"no labels", withLabels(nil), plain, false},
		{"app name alone, not renamed", withLabels(map[string]string{appNameLabel: appNameValue}), plain, false},
		{"no labels, renamed", withLabels(nil), renamed, false},
		{"app name alone, renamed (predates the installed-by label)", withLabels(map[string]string{appNameLabel: appNameValue}), renamed, true},
		{"installed-by, renamed", withLabels(map[string]string{InstalledByLabel: InstalledByValue}), renamed, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := owned(tc.live, tc.cand); got != tc.expect {
				t.Errorf("owned = %v, want %v", got, tc.expect)
			}
		})
	}
}

// The prunable kinds are the ones the installer may delete. role.yaml is the installer's RBAC
// (ADR 0058): every prunable kind must have a delete rule there, or a prune would be refused
// mid-run and roll the upgrade back.
func TestPrunableKindsHaveDeleteRight(t *testing.T) {
	raw, err := os.ReadFile("../../config/rbac/ichor-installer/role.yaml")
	if err != nil {
		t.Fatal(err)
	}

	objs, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}

	var deletable []rbacv1.PolicyRule

	for _, o := range objs {
		if o.GetKind() != kindClusterRole && o.GetKind() != kindRole {
			continue
		}

		var role rbacv1.ClusterRole
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.Object, &role); err != nil {
			t.Fatalf("reading %s %s: %v", o.GetKind(), o.GetName(), err)
		}

		for _, r := range role.Rules {
			if slices.Contains(r.Verbs, "delete") {
				deletable = append(deletable, r)
			}
		}
	}

	for gk, resource := range prunableKinds {
		granted := slices.ContainsFunc(deletable, func(r rbacv1.PolicyRule) bool {
			return slices.Contains(r.APIGroups, gk.Group) && slices.Contains(r.Resources, resource)
		})
		if !granted {
			t.Errorf("%s is prunable but config/rbac/ichor-installer grants no delete on %s in group %q", gk.Kind, resource, gk.Group)
		}
	}
}

// withObjects appends documents to a fixture manifest.
func withObjects(base []byte, docs ...string) []byte {
	out := append([]byte{}, base...)
	for _, d := range docs {
		out = append(out, "---\n"+d...)
	}

	return out
}

func roleDoc(ns, name string) string {
	return fmt.Sprintf("apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: %s\n  namespace: %s\nrules: []\n", name, ns)
}

// gadgetCRD is a second CRD: an earlier run may install one the new manifest drops, and it must stay.
func (f fixture) gadgetCRD() string {
	return fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: gadgets.%s
spec:
  group: %s
  names: {kind: Gadget, listKind: GadgetList, plural: gadgets, singular: gadget}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema: {type: object}
`, f.group, f.group)
}

func runInstaller(t *testing.T, f fixture, runID string, manifest []byte) {
	t.Helper()

	var order []string

	if err := Run(context.Background(), k8sClient, Options{Namespace: f.ns, RunID: runID, Manifest: manifest, Ready: noWait(&order)}); err != nil {
		t.Fatalf("Run %s: %v", runID, err)
	}
}

func exists(t *testing.T, apiVersion, kind, ns, name string) bool {
	t.Helper()

	o := &unstructured.Unstructured{}
	o.SetAPIVersion(apiVersion)
	o.SetKind(kind)

	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, o)
	if apierrors.IsNotFound(err) {
		return false
	}

	if err != nil {
		t.Fatalf("getting %s %s: %v", kind, name, err)
	}

	return true
}

// setLabel sets key to value on an existing object, or removes key when value is empty.
func setLabel(t *testing.T, o client.Object, key, value string) {
	t.Helper()

	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(o), o); err != nil {
		t.Fatalf("getting %s: %v", o.GetName(), err)
	}

	labels := o.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}

	if value == "" {
		delete(labels, key)
	} else {
		labels[key] = value
	}

	o.SetLabels(labels)

	if err := k8sClient.Update(context.Background(), o); err != nil {
		t.Fatalf("updating %s: %v", o.GetName(), err)
	}
}

// TestRunPrunesWhatAnEarlierRunInstalledAndTheManifestDrops runs the installer twice. The first
// run installs a stale Role, a Role the installer does not own (its label is removed), a Secret
// with the installed-by label, and a second CRD. The second run ships none of them: only the
// labelled Role goes.
func TestRunPrunesWhatAnEarlierRunInstalledAndTheManifestDrops(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	runInstaller(t, f, f.run+"-1", withObjects(f.manifest("ctrl:v1", ""),
		roleDoc(f.ns, "stale-role"), roleDoc(f.ns, "unowned-role"), f.gadgetCRD()))

	state := &corev1.Secret{}
	state.Namespace, state.Name = f.ns, "state"
	state.Labels = map[string]string{InstalledByLabel: InstalledByValue}

	if err := k8sClient.Create(ctx, state); err != nil {
		t.Fatal(err)
	}

	role := &rbacv1.Role{}
	role.Namespace, role.Name = f.ns, "unowned-role"
	setLabel(t, role, InstalledByLabel, "")

	runInstaller(t, f, f.run+"-2", f.manifest("ctrl:v2", ""))

	if exists(t, rbacAPI, kindRole, f.ns, "stale-role") {
		t.Error("stale-role was installed by the installer and is no longer shipped: it must be pruned")
	}

	if !exists(t, rbacAPI, kindRole, f.ns, "unowned-role") {
		t.Error("unowned-role lost the installed-by label: it must be kept")
	}

	if !exists(t, "v1", "Secret", f.ns, "state") {
		t.Error("a Secret is never pruned")
	}

	if !exists(t, "apiextensions.k8s.io/v1", "CustomResourceDefinition", "", "gadgets."+f.group) {
		t.Error("a CRD is never pruned: deleting it deletes its custom resources")
	}

	refs, err := readInventory(ctx, k8sClient, f.ns, io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range refs {
		if r.Name == "stale-role" {
			t.Error("the inventory recorded by the second run still lists stale-role")
		}
	}

	if !slices.ContainsFunc(refs, func(r ref) bool { return r.Name == "ichor-controller-manager" }) {
		t.Errorf("the inventory = %v, want the controller Deployment recorded", refs)
	}
}

// TestRunRemovesTheRenamedOverrideRoleOnlyWhenItIsIchors: ADR 0109 renamed the break-glass
// ClusterRole. The old object goes when it carries the app name label Ichor's build put on it,
// and stays when a person made one with the same name.
func TestRunRemovesTheRenamedOverrideRoleOnlyWhenItIsIchors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	const old = breakGlassRole

	cr := &rbacv1.ClusterRole{}
	cr.Name = old
	if err := k8sClient.Create(ctx, cr); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := k8sClient.Delete(context.Background(), cr); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleaning up %s: %v", old, err)
		}
	})

	runInstaller(t, f, f.run+"-1", f.manifest("ctrl:v1", ""))

	if !exists(t, rbacAPI, kindClusterRole, "", old) {
		t.Error("a ClusterRole with the old name that Ichor did not install must be kept")
	}

	setLabel(t, cr, appNameLabel, appNameValue)

	runInstaller(t, f, f.run+"-2", f.manifest("ctrl:v2", ""))

	if exists(t, rbacAPI, kindClusterRole, "", old) {
		t.Errorf("%s carries app.kubernetes.io/name=ichor and the new manifest renames it: it must be pruned", old)
	}
}
