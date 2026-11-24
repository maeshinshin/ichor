package installer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// retiredDocs is a v0.2.4 patch document that sets a registry's client identity, the field ADR 0152 retires.
const retiredDocs = `[{"kind":"Config","config":{"version":"v1alpha1","machine":{"registries":{"config":{"r.example":{"tls":{"clientIdentity":{"crt":"eA==","key":"eA=="}}}}}}}}]`

// The installer Job reads stored patches with the rights of the release that is running, not the target's. v0.2.4's
// role grants no `list` on talosconfigpatches, so the read is forbidden and must not refuse the upgrade (Run reads
// again once the new role is effective); the current ClusterRole lists every namespace, and a patch in another
// namespace than the installer's is refused.
func TestCheckReadsStoredPatchesUnderTheInstallerRole(t *testing.T) {
	cases := map[string]struct {
		role    string
		binding string // the binding file, when the role file has none
		want    string // "" means no refusal
	}{
		"the v0.2.4 role, which cannot list patches":    {previousInstallerRBAC, "", ""},
		"the current role, which lists every namespace": {"../../config/rbac/ichor-installer/role.yaml", "../../config/rbac/ichor-installer/role_binding.yaml", "ADR 0152"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)

			installOldCRD(t, loadCRD(t, "testdata/talosconfigpatches-v0.2.4.yaml"))

			var docs []any
			if err := json.Unmarshal([]byte(retiredDocs), &docs); err != nil {
				t.Fatal(err)
			}

			patch := storedPatch("reg", docs)
			// In another namespace than the installer's: only a cluster-wide read sees it.
			patch.SetNamespace(otherNamespace(t))
			createWhenEstablished(t, patch)

			for _, doc := range []string{rbac(t, f.ns, tc.role, tc.binding), upgradeDeployments(f.ns, `"--external-url=https://old.example.test"`)} {
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
			cfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + f.ns + ":ichor-installer"}

			c, err := client.New(cfg, client.Options{Scheme: k8sClient.Scheme()})
			if err != nil {
				t.Fatal(err)
			}

			if err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				return c.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: installerRoleName}, untyped("rbac.authorization.k8s.io/v1", kindRole)) == nil, nil
			}); err != nil {
				t.Fatalf("the installer role never became effective: %v", err)
			}

			err = Check(ctx, c, Options{Namespace: f.ns, Manifest: targetManifest(t, f.ns, `"--external-url=https://old.example.test"`)})

			switch {
			case tc.want == "" && err != nil:
				t.Errorf("Check refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("want a refusal containing %q, got %v", tc.want, err)
			}
		})
	}
}

func rbac(t *testing.T, ns, role, binding string) string {
	t.Helper()

	if binding == "" {
		return string(installerRBACFor(t, role, ns))
	}

	return string(installerRBACFor(t, role, ns)) + "---\n" + string(installerRBACFor(t, binding, ns))
}

// A UI-driven upgrade from v0.2.4: the pre-RBAC read is forbidden and tolerated, and Run reads again, cluster-wide,
// once the new ClusterRole is effective, so a patch in another namespace still refuses it (and the RBAC is rolled back).
func TestRunFromV024RefusesAStoredRetiredFieldInAnotherNamespace(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	installOldCRD(t, loadCRD(t, "testdata/talosconfigpatches-v0.2.4.yaml"))

	var docs []any
	if err := json.Unmarshal([]byte(retiredDocs), &docs); err != nil {
		t.Fatal(err)
	}

	other := otherNamespace(t)

	patch := storedPatch("reg", docs)
	patch.SetNamespace(other)
	createWhenEstablished(t, patch)

	for _, doc := range []string{rbac(t, f.ns, previousInstallerRBAC, ""), upgradeDeployments(f.ns, `"--external-url=https://old.example.test"`)} {
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
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "system:serviceaccount:" + f.ns + ":ichor-installer"}

	c, err := client.New(cfg, client.Options{Scheme: k8sClient.Scheme()})
	if err != nil {
		t.Fatal(err)
	}

	if err := wait.PollUntilContextTimeout(ctx, 200*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		return c.Get(ctx, types.NamespacedName{Namespace: f.ns, Name: installerRoleName}, untyped("rbac.authorization.k8s.io/v1", kindRole)) == nil, nil
	}); err != nil {
		t.Fatalf("the previous installer role never became effective: %v", err)
	}

	var order []string

	err = Run(ctx, c, Options{
		Namespace: f.ns, RunID: f.run, Manifest: targetManifest(t, f.ns, `"--external-url=https://old.example.test"`),
		Ready: noWait(&order), RBACSettle: 20 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "ADR 0152") {
		t.Fatalf("want Run to refuse on the patch in %s, got %v", other, err)
	}
}

// otherNamespace creates a namespace that is not the installer's.
func otherNamespace(t *testing.T) string {
	t.Helper()

	ns := &corev1.Namespace{}
	ns.GenerateName = "installer-other-"

	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}

	return ns.Name
}
