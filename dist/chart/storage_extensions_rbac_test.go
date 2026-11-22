package chart

import "testing"

const (
	storageExtensionsConfigDir = "../../config/rbac/ichor-storage-extensions/"
	storageExtensionsSA        = "ichor-storage-extensions"
	verbPatch                  = "patch"
)

// TestStorageExtensionsIdentity pins ADR 0173: the write of spec.schematic.id is made by a ServiceAccount of its
// own, not by the controller and not by ichor-auto-patch. The controller impersonates that one account by name;
// the account holds patch on talosclusters and nothing else; and the auto-patch account gains nothing from it.
// The chart and config/rbac say the same.
func TestStorageExtensionsIdentity(t *testing.T) {
	docs := renderChart(t, "ichor")

	if find(docs, kindServiceAccount, storageExtensionsSA) == nil {
		t.Fatalf("the chart renders no ServiceAccount %s", storageExtensionsSA)
	}

	for _, name := range []string{managerRole, managerSecretsRole} {
		for tr := range rbacTriples(allRules(find(docs, kindClusterRole, name))) {
			reads := tr.verb == verbGetController || tr.verb == verbList || tr.verb == verbWatch
			if tr.group == ichorGroup && tr.resource == resClusters && !reads {
				t.Errorf("%s grants %s: the controller writes no TalosCluster, it impersonates %s", name, tr, storageExtensionsSA)
			}
		}
	}

	imp := find(docs, kindRole, "ichor-storage-extensions-impersonate-role")
	if imp == nil {
		t.Fatal("no Role ichor-storage-extensions-impersonate-role")
	}

	want := rbacTriple{"", resServiceAccounts, verbImpersonate, storageExtensionsSA}
	if got := rbacTriples(allRules(imp)); len(got) != 1 || !got[want] {
		t.Errorf("the impersonation Role grants %v, want impersonate on %s only", got, storageExtensionsSA)
	}

	role := find(docs, kindClusterRole, "ichor-storage-extensions-role")
	if got := rbacTriples(allRules(role)); len(got) != 1 || !got[rbacTriple{ichorGroup, resClusters, verbPatch, ""}] {
		t.Errorf("ichor-storage-extensions-role grants %v, want patch on talosclusters only", got)
	}

	// The two delegated identities stay apart: auto-patch holds no patch, and this one no get, update or create.
	for tr := range rbacTriples(allRules(find(docs, kindClusterRole, "ichor-auto-patch-role"))) {
		if tr.verb == verbPatch {
			t.Errorf("ichor-auto-patch-role grants %s: the schematic write belongs to %s", tr, storageExtensionsSA)
		}
	}

	configDocs := loadYAMLDocs(t, storageExtensionsConfigDir+"role.yaml")
	const chartFile = "dist/chart/templates/rbac/ichor-storage-extensions.yaml"

	checkRBACRulesMatch(t, "ichor", "ClusterRole storage-extensions-role",
		find(configDocs, kindClusterRole, "storage-extensions-role"), role, storageExtensionsConfigDir+"role.yaml", chartFile)

	impDocs := loadYAMLDocs(t, storageExtensionsConfigDir+"impersonate_role.yaml")
	impFile := storageExtensionsConfigDir + "impersonate_role.yaml"
	checkRBACRulesMatch(t, "ichor", "Role storage-extensions-impersonate-role",
		find(impDocs, kindRole, "storage-extensions-impersonate-role"), imp, impFile, chartFile)
}
