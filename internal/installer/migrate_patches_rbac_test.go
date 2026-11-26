package installer

import (
	"context"
	"io"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MigratePatches is the installer's last step, so it runs under the Role and ClusterRole of the release being installed
// as the RBAC stage applied them. Fake clients and an admin client cannot see a verb the Role lacks.
func TestMigratePatchesUnderTheInstallerRole(t *testing.T) {
	migrateUnderTheInstallerRole(t, "")
}

// A patch and its cluster in a namespace the installer has no Role in: the cluster-wide list on both kinds reaches them.
func TestMigratePatchesInAnotherNamespaceUnderTheInstallerRole(t *testing.T) {
	migrateUnderTheInstallerRole(t, "other")
}

// otherNS, when set, is a namespace of its own for the patch and its cluster, and MigratePatches is given no namespace.
func migrateUnderTheInstallerRole(t *testing.T, otherNS string) {
	t.Helper()

	ctx := context.Background()
	f := newFixture(t)
	objNS, listNS := f.ns, f.ns

	installOldCRD(t, loadCRD(t, "../../config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml"))
	installOldCRD(t, loadCRD(t, "../../config/crd/bases/ichor.maesh.dev_talosclusters.yaml"))

	// Its own client: k8sClient would keep the TalosCluster mapping after the CRD is removed and break the tests after this one.
	admin, err := client.New(testCfg, client.Options{Scheme: k8sClient.Scheme()})
	if err != nil {
		t.Fatal(err)
	}

	if otherNS != "" {
		objNS, listNS = otherNS+"-"+f.ns, ""
		ns := untyped("v1", "Namespace")
		ns.SetName(objNS)
		createIn(t, admin, ns)
	}

	patch := storedPatch("p", certSANDocs)
	patch.SetNamespace(objNS)
	createIn(t, admin, patch)

	cl := storedCluster("c", "v1.14")
	cl.SetNamespace(objNS)
	// shortcut: only what the CRD requires; the installer reads versionContract and nothing else.
	cl.Object["spec"] = map[string]any{
		kVersionContract: "v1.14", "talosVersion": "v1.14.0", "kubernetesVersion": "v1.36.0",
		"controlPlane": map[string]any{"count": int64(1), "schedulable": "true"},
		"endpoint":     map[string]any{"address": "10.0.0.1", "type": "node"},
		"secretsRef":   map[string]any{kName: "s"},
	}
	createIn(t, admin, cl)

	objs, err := Decode([]byte(rbac(t, f.ns, "../../config/rbac/ichor-installer/role.yaml", "../../config/rbac/ichor-installer/role_binding.yaml")))
	if err != nil {
		t.Fatal(err)
	}

	for _, obj := range objs {
		if err := ApplyOne(ctx, k8sClient, obj); err != nil {
			t.Fatalf("seeding %s %s: %v", obj.GetKind(), obj.GetName(), err)
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

	res, err := MigratePatches(ctx, c, listNS, io.Discard)
	if err != nil || len(res.Migrated) != 1 || len(res.Failed) != 0 {
		t.Fatalf("want one patch migrated, got %+v, %v", res, err)
	}

	p := getPatchIn(t, admin, objNS, "p")
	if len(p.Spec.Documents) != 0 || p.Spec.Config == nil || migratedMessage(p, "True") == "" {
		t.Errorf("want Migrated=True and spec.config set, got %+v", p)
	}
}

// createIn is createWhenEstablished through c.
func createIn(t *testing.T, c client.Client, obj *unstructured.Unstructured) {
	t.Helper()

	var err error

	for range 50 {
		if err = c.Create(context.Background(), obj); err == nil {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("creating the %s: %v", obj.GetKind(), err)
}
