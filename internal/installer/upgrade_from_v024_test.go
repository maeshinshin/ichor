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
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// previousInstallerRBAC is the installer RBAC release v0.2.4 ships, pinned from git: what a cluster
// running v0.2.4 holds when its upgrade starts. The live role changes; this one does not.
const previousInstallerRBAC = "testdata/installer-rbac-v0.2.4.yaml"

// installerRoleName is the installer's namespaced Role, the one every release names.
const installerRoleName = "ichor-installer-role"

// installerRBACFor renders an installer RBAC file (kustomize form) into namespace ns: the names
// the installer's own prefix uses, the subject's and the Role's namespace, and the namespaces rule.
func installerRBACFor(t *testing.T, path, ns string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return []byte(strings.NewReplacer(
		"name: installer-rolebinding", "name: ichor-installer-rolebinding",
		"name: installer-role", "name: ichor-installer-role",
		"name: installer\n", "name: ichor-installer\n",
		"namespace: system", "namespace: "+ns,
		"ichor-system", ns,
	).Replace(string(raw)))
}

// upgradeDeployments are the two Deployments init customizes, found by container name: ichor-ui with
// uiArgs, and the controller.
func upgradeDeployments(ns, uiArgs string) string {
	dep := func(name, container, args string) string {
		return fmt.Sprintf(`---
apiVersion: apps/v1
kind: Deployment
metadata: {name: %[1]s, namespace: %[2]s}
spec:
  selector:
    matchLabels: {app: %[1]s}
  template:
    metadata:
      labels: {app: %[1]s}
    spec:
      containers:
      - name: %[3]s
        image: ichor:v2
        args: [%[4]s]
`, name, ns, container, args)
	}

	return dep("ichor-controller-manager", ControllerContainer, `"--oidc-refresh-lifetime=12h"`) +
		dep("ichor-ichor-ui", UIContainer, uiArgs)
}

// targetManifest is the target release's manifest for this test: the two Deployments and its installer RBAC.
func targetManifest(t *testing.T, ns, uiArgs string) []byte {
	t.Helper()

	return []byte(upgradeDeployments(ns, uiArgs) +
		"---\n" + string(installerRBACFor(t, "../../config/rbac/ichor-installer/role.yaml", ns)) +
		"---\n" + string(installerRBACFor(t, "../../config/rbac/ichor-installer/role_binding.yaml", ns)))
}

// untyped is an object of apiVersion and kind with no content, to Get into.
func untyped(apiVersion, kind string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)

	return u
}

// TestUpgradeFromV024RBACReadsTheIssuerURLUnderTheRoleItInstalls runs an upgrade as v0.2.4's installer has
// it: a live ichor-ui and controller (so the issuer read is reached), a TalosCluster that enables the
// issuer with a new URL, and nothing but v0.2.4's rights. The upgrade must apply the new URL to ichor-ui.
func TestUpgradeFromV024RBACReadsTheIssuerURLUnderTheRoleItInstalls(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	user := "system:serviceaccount:" + f.ns + ":ichor-installer"

	seed := func(doc []byte) {
		objs, err := Decode(doc)
		if err != nil {
			t.Fatal(err)
		}

		for _, obj := range objs {
			if err := ApplyOne(ctx, k8sClient, obj); err != nil {
				t.Fatalf("seeding %s %s: %v", obj.GetKind(), obj.GetName(), err)
			}
		}
	}

	// The TalosCluster CRD: without it the list is answered by the RESTMapper and never reaches RBAC.
	seed([]byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: talosclusters.ichor.maesh.dev}
spec:
  group: ichor.maesh.dev
  names: {kind: TalosCluster, listKind: TalosClusterList, plural: talosclusters, singular: taloscluster}
  scope: Namespaced
  versions:
  - name: v1alpha1
    served: true
    storage: true
    schema:
      openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}
`))

	seed(installerRBACFor(t, previousInstallerRBAC, f.ns))

	// The live ichor-ui of v0.2.4: signed in, with the issuer the operator set at init.
	seed([]byte(upgradeDeployments(f.ns, `"--external-url=https://old.example.test"`)))

	// The TalosCluster that enables the issuer, now with a new URL. The CRD is established a moment after it is applied.
	tc := talosCluster("example", true, "https://new.example.test")
	tc.SetNamespace(f.ns)

	if err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		return k8sClient.Create(ctx, tc.DeepCopy()) == nil, nil
	}); err != nil {
		t.Fatalf("the TalosCluster CRD never became established: %v", err)
	}

	cfg := rest.CopyConfig(testCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}

	c, err := client.New(cfg, client.Options{Scheme: k8sClient.Scheme()})
	if err != nil {
		t.Fatal(err)
	}

	if err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		return c.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: installerRoleName}, untyped("rbac.authorization.k8s.io/v1", kindRole)) == nil, nil
	}); err != nil {
		t.Fatalf("the previous installer role never became effective: %v", err)
	}

	list := &unstructured.UnstructuredList{}
	list.SetAPIVersion("ichor.maesh.dev/v1alpha1")
	list.SetKind("TalosClusterList")

	if err := c.List(ctx, list, client.InNamespace(f.ns)); !apierrors.IsForbidden(err) {
		t.Fatalf("precondition: the v0.2.4 role must be forbidden to list TalosClusters, got %v", err)
	}

	var order []string

	err = Run(ctx, c, Options{
		Namespace: f.ns, RunID: f.run, Manifest: targetManifest(t, f.ns, `"--external-url=https://old.example.test"`),
		Ready: noWait(&order), RBACSettle: 20 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run under the v0.2.4 installer role: %v", err)
	}

	ui := untyped("apps/v1", kindDeployment)
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: "ichor-ichor-ui"}, ui); err != nil {
		t.Fatal(err)
	}

	if spec := fmt.Sprint(ui.Object[kSpec]); !strings.Contains(spec, "--external-url=https://new.example.test") {
		t.Errorf("the TalosCluster's issuer URL must reach ichor-ui, got spec %s", spec)
	}
}
