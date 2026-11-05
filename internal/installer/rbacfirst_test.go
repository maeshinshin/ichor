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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// An upgrade from an older release runs under that release's installer role. These tests give the
// installer exactly such a role (no rule for a kind only the new manifest has) and check the
// installer widens its own role first, before it reads or writes anything else.

// oldInstaller is a fixture whose installer ServiceAccount holds the old role.
type oldInstaller struct {
	fixture
	client   client.Client // authenticated as the installer ServiceAccount
	gizmoGrp string
	// clusterRole names the old installer's ClusterRole and ClusterRoleBinding; the new manifest
	// updates both in place.
	clusterRole string
}

const oldRoleRules = `
- apiGroups: [""]
  resources: [configmaps]
  verbs: [create, get, list, delete]
- apiGroups: [apps]
  resources: [deployments]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [roles, rolebindings]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [roles]
  verbs: [bind, escalate]
`

func newOldInstaller(t *testing.T) oldInstaller {
	t.Helper()

	ctx := context.Background()
	f := newFixture(t)
	o := oldInstaller{fixture: f, gizmoGrp: "gizmo." + f.group, clusterRole: "ichor-installer-old-" + f.ns}
	user := "system:serviceaccount:" + f.ns + ":ichor-installer"

	// A stand-in for CiliumNetworkPolicy: a namespaced kind whose CRD already exists.
	crd := fmt.Sprintf(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: gizmos.%[1]s}
spec:
  group: %[1]s
  names: {kind: Gizmo, listKind: GizmoList, plural: gizmos, singular: gizmo}
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}
`, o.gizmoGrp)

	// The cluster-scoped part of the old role: reading and patching CRDs, and rewriting its own
	// ClusterRole (every release holds get/create/patch/update, bind and escalate on clusterroles).
	cluster := fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: %[1]s}
rules:
- apiGroups: [apiextensions.k8s.io]
  resources: [customresourcedefinitions]
  verbs: [create, get, patch, update]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [clusterroles, clusterrolebindings]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [clusterroles]
  verbs: [bind, escalate]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: %[1]s}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: %[1]s}
subjects: [{apiGroup: rbac.authorization.k8s.io, kind: User, name: %[2]s}]
`, o.clusterRole, user)

	oldNamespaced := o.installerRBAC(oldRoleRules)

	for _, doc := range []string{crd, cluster, oldNamespaced} {
		objs, err := Decode([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}

		for _, obj := range objs {
			if err := applyOne(ctx, k8sClient, obj); err != nil {
				t.Fatalf("seeding %s %s: %v", obj.GetKind(), obj.GetName(), err)
			}
		}
	}

	cfg := rest.CopyConfig(testCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}

	c, err := client.New(cfg, client.Options{Scheme: k8sClient.Scheme()})
	if err != nil {
		t.Fatal(err)
	}

	o.client = c

	// The old role is effective, and it really does not cover Gizmos.
	if err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		return c.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: "ichor-installer-role"}, o.unstructured("rbac.authorization.k8s.io/v1", "Role")) == nil, nil
	}); err != nil {
		t.Fatalf("the old installer role never became effective: %v", err)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: "x"}, o.gizmo()); !apierrors.IsForbidden(err) {
		t.Fatalf("precondition: the old role must be forbidden to read gizmos, got %v", err)
	}

	return o
}

func (o oldInstaller) unstructured(apiVersion, kind string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)

	return u
}

func (o oldInstaller) gizmo() *unstructured.Unstructured {
	return o.unstructured(o.gizmoGrp+"/v1", "Gizmo")
}

// installerRBAC renders the installer's namespaced Role and RoleBinding with rules.
func (o oldInstaller) installerRBAC(rules string) string {
	return fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: ichor-installer-role, namespace: %[1]s}
rules:%[2]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ichor-installer-rolebinding, namespace: %[1]s}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: ichor-installer-role}
subjects: [{apiGroup: rbac.authorization.k8s.io, kind: User, name: system:serviceaccount:%[1]s:ichor-installer}]
`, o.ns, rules)
}

// newManifest is the fixture manifest plus the new installer role, which also grants gizmos,
// and a Gizmo (the kind the old role lacks), emitted after the role like kustomize may.
func (o oldInstaller) newManifest() []byte {
	rules := oldRoleRules + fmt.Sprintf(`- apiGroups: [%s]
  resources: [gizmos]
  verbs: [create, get, patch, update, delete]
`, o.gizmoGrp)

	gizmo := fmt.Sprintf(`---
apiVersion: %s/v1
kind: Gizmo
metadata: {name: ichor-cni, namespace: %s}
spec: {a: b}
`, o.gizmoGrp, o.ns)

	return append(append(o.manifest("ctrl:v2", ""), []byte(o.installerRBAC(rules))...), []byte(gizmo)...)
}

func TestRunUpgradesUnderAnOldInstallerRoleWithoutTheNewKindsRule(t *testing.T) {
	o := newOldInstaller(t)

	var order []string

	err := Run(context.Background(), o.client, Options{
		Namespace: o.ns, RunID: o.run, Manifest: o.newManifest(), Ready: noWait(&order),
		RBACSettle: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: o.ns, Name: "ichor-cni"}, o.gizmo()); err != nil {
		t.Errorf("the Gizmo of the new manifest was not applied: %v", err)
	}

	if len(order) == 0 || order[0] != "Role/ichor-installer-role" {
		t.Errorf("the installer RBAC must be applied first, post-check order = %v", order)
	}

	// The snapshot has the pre-upgrade role first, and records the Gizmo as absent.
	snap, err := loadSnapshot(context.Background(), k8sClient, o.ns, o.run)
	if err != nil {
		t.Fatal(err)
	}

	if len(snap) == 0 || snap[0].Kind != kindRole || strings.Contains(fmt.Sprint(snap[0].Object["rules"]), "gizmos") {
		t.Errorf("the snapshot must start with the old role, got %+v", snap)
	}

	var sawGizmo bool

	for _, e := range snap {
		if e.Kind == "Gizmo" {
			sawGizmo = true

			if e.Object != nil {
				t.Error("the Gizmo did not exist before the upgrade")
			}
		}
	}

	if !sawGizmo {
		t.Error("the Gizmo must be in the snapshot")
	}
}

func TestRollbackRestoresTheInstallerRBACSnapshot(t *testing.T) {
	o := newOldInstaller(t)

	err := Run(context.Background(), o.client, Options{
		Namespace: o.ns, RunID: o.run, Manifest: o.newManifest(), Ready: failOn("ichor-ui", "UI never became ready"), RBACSettle: 20 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Run error = %v, want a rollback", err)
	}

	role := o.unstructured("rbac.authorization.k8s.io/v1", "Role")
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: o.ns, Name: "ichor-installer-role"}, role); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(fmt.Sprint(role.Object["rules"]), "gizmos") {
		t.Errorf("the rollback must restore the old role, got rules %v", role.Object["rules"])
	}

	// The Gizmo the failed run created is gone again.
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: o.ns, Name: "ichor-cni"}, o.gizmo()); !apierrors.IsNotFound(err) {
		t.Errorf("the Gizmo must be deleted by the rollback, got %v", err)
	}
}

// A new release adds an installer Role in a namespace the old installer has no rights in (the
// kube-system Role of commit 8a7199b). The old installer cannot even read it, so the rights must
// arrive through the cluster-scoped role, which stage 0 applies before it touches that namespace.
func TestRunUpgradeReachesANewNamespaceThroughTheClusterRole(t *testing.T) {
	ctx := context.Background()
	o := newOldInstaller(t)

	other := &corev1.Namespace{GenerateName: "installer-other-"}
	if err := k8sClient.Create(ctx, other); err != nil {
		t.Fatal(err)
	}

	roleKey := types.NamespacedName{Namespace: other.Name, Name: "ichor-installer-other-role"}
	if err := o.client.Get(ctx, roleKey, o.unstructured("rbac.authorization.k8s.io/v1", "Role")); !apierrors.IsForbidden(err) {
		t.Fatalf("precondition: the old installer must have no rights in %s, got %v", other.Name, err)
	}

	// The new ClusterRole keeps the old rules and adds the folded namespaced ones.
	manifest := append(o.manifest("ctrl:v2", ""), []byte(fmt.Sprintf(`---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: %[1]s}
rules:
- apiGroups: [apiextensions.k8s.io]
  resources: [customresourcedefinitions]
  verbs: [create, get, patch, update]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [clusterroles, clusterrolebindings]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [clusterroles]
  verbs: [bind, escalate]
- apiGroups: [""]
  resources: [serviceaccounts]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [roles, rolebindings]
  verbs: [create, get, patch, update, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [roles]
  verbs: [bind, escalate]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: %[1]s}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: %[1]s}
subjects: [{apiGroup: rbac.authorization.k8s.io, kind: User, name: system:serviceaccount:%[2]s:ichor-installer}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: ichor-installer-other-role, namespace: %[3]s}
rules:
- apiGroups: [""]
  resources: [configmaps]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: ichor-installer-other-rolebinding, namespace: %[3]s}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: ichor-installer-other-role}
subjects: [{apiGroup: rbac.authorization.k8s.io, kind: User, name: system:serviceaccount:%[2]s:ichor-installer}]
---
apiVersion: v1
kind: ServiceAccount
metadata: {name: other-sa, namespace: %[3]s}
`, o.clusterRole, o.ns, other.Name))...)

	var order []string

	err := Run(ctx, o.client, Options{
		Namespace: o.ns, RunID: o.run, Manifest: manifest, Ready: noWait(&order), RBACSettle: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := k8sClient.Get(ctx, roleKey, o.unstructured("rbac.authorization.k8s.io/v1", "Role")); err != nil {
		t.Errorf("the new namespaced installer Role was not applied: %v", err)
	}

	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: other.Name, Name: "other-sa"}, o.unstructured("v1", "ServiceAccount")); err != nil {
		t.Errorf("the ServiceAccount in the new namespace was not applied: %v", err)
	}

	if len(order) == 0 || order[0] != "ClusterRole/"+o.clusterRole {
		t.Errorf("the cluster-scoped RBAC must be applied first, post-check order = %v", order)
	}
}

func TestSnapshotFailsOnForbiddenButNotOnAMissingKind(t *testing.T) {
	o := newOldInstaller(t)

	gizmo := o.gizmo()
	gizmo.SetNamespace(o.ns)
	gizmo.SetName("g")

	if _, err := takeSnapshot(context.Background(), o.client, o.ns, o.run+"-forbidden", 1, []*unstructured.Unstructured{gizmo}); err == nil ||
		!apierrors.IsForbidden(errors.Unwrap(err)) {
		t.Errorf("a Forbidden read must fail the snapshot, got %v", err)
	}

	missing := o.unstructured("nosuch."+o.group+"/v1", "Nothing")
	missing.SetNamespace(o.ns)
	missing.SetName("n")

	if _, err := takeSnapshot(context.Background(), k8sClient, o.ns, o.run+"-missing", 1, []*unstructured.Unstructured{missing}); err != nil {
		t.Errorf("a kind that is not installed means nothing to snapshot, got %v", err)
	}
}
