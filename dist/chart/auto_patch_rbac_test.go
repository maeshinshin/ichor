package chart

import "testing"

const (
	resClusters = "talosclusters"
	verbList    = "list"
	verbWatch   = "watch"
)

const autoPatchConfigDir = "../../config/rbac/ichor-auto-patch/"

// TestAutoPatchIdentity pins ADR 0116: the controller never writes a TalosCluster spec, so its own ClusterRole
// holds no update or patch on talosclusters; it may only impersonate the ServiceAccount ichor-auto-patch, by
// name; and that ServiceAccount holds get and update on talosclusters and create on talosoperations, nothing
// more. The chart and config/rbac say the same.
func TestAutoPatchIdentity(t *testing.T) {
	docs := renderChart(t, "ichor")

	if find(docs, kindServiceAccount, "ichor-auto-patch") == nil {
		t.Fatal("the chart renders no ServiceAccount ichor-auto-patch, the user the controller impersonates")
	}

	managers := 0

	for _, d := range docs {
		isManager := nameOf(d) == "ichor-manager-role" || nameOf(d) == "ichor-manager-secrets-role"
		if !isManager || (d["kind"] != kindClusterRole && d["kind"] != kindRole) {
			continue
		}

		managers++

		for tr := range rbacTriples(allRules(d)) {
			reads := tr.verb == verbGetController || tr.verb == verbList || tr.verb == verbWatch
			if tr.group == ichorGroup && tr.resource == resClusters && !reads {
				t.Errorf("%s grants %s: the controller never writes a TalosCluster", nameOf(d), tr)
			}

			if tr.verb == verbImpersonate {
				t.Errorf("%s grants %s: its impersonation is the namespaced Role of its own", nameOf(d), tr)
			}
		}
	}

	if managers == 0 {
		t.Fatal("found no manager role to check")
	}

	imp := find(docs, kindRole, "ichor-auto-patch-impersonate-role")
	if imp == nil {
		t.Fatal("no Role ichor-auto-patch-impersonate-role")
	}

	got := rbacTriples(allRules(imp))
	if len(got) != 1 || !got[rbacTriple{"", "serviceaccounts", verbImpersonate, "ichor-auto-patch"}] {
		t.Errorf("the impersonation Role grants %v, want impersonate on ichor-auto-patch only", got)
	}

	role := find(docs, kindClusterRole, "ichor-auto-patch-role")
	want := map[rbacTriple]bool{
		{ichorGroup, resClusters, verbGetController, ""}:          true,
		{ichorGroup, resClusters, verbUpdate, ""}:                 true,
		{ichorGroup, "talosoperations", verbCreateController, ""}: true,
	}

	if g := rbacTriples(allRules(role)); len(g) != len(want) {
		t.Errorf("ichor-auto-patch-role grants %v, want %v", g, want)
	} else {
		for k := range want {
			if !g[k] {
				t.Errorf("ichor-auto-patch-role lacks %s", k)
			}
		}
	}

	configDocs := loadYAMLDocs(t, autoPatchConfigDir+"role.yaml")
	const chartFile = "dist/chart/templates/rbac/ichor-auto-patch.yaml"

	checkRBACRulesMatch(t, "ichor", "ClusterRole auto-patch-role",
		find(configDocs, kindClusterRole, "auto-patch-role"), role, autoPatchConfigDir+"role.yaml", chartFile)

	impDocs := loadYAMLDocs(t, autoPatchConfigDir+"impersonate_role.yaml")
	checkRBACRulesMatch(t, "ichor", "Role auto-patch-impersonate-role",
		find(impDocs, kindRole, "auto-patch-impersonate-role"), imp, autoPatchConfigDir+"impersonate_role.yaml", chartFile)
}
