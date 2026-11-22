package chart

import "testing"

const (
	consentSweepConfigDir = "../../config/rbac/ichor-consent-sweep/"
	consentSweepSA        = "ichor-consent-sweep"
	managerRole           = "ichor-manager-role"
	managerSecretsRole    = "ichor-manager-secrets-role"
	resServiceAccounts    = "serviceaccounts"
)

// TestConsentSweepIdentity pins ADR 0066's 2026-11-22 amendment: the removal of the kube-proxy switch consent
// annotation is made by a ServiceAccount of its own, not by the controller. The controller impersonates that one
// account by name, and the account holds patch on talosclusters and nothing else. The chart and config/rbac say
// the same.
func TestConsentSweepIdentity(t *testing.T) {
	docs := renderChart(t, "ichor")

	if find(docs, kindServiceAccount, consentSweepSA) == nil {
		t.Fatalf("the chart renders no ServiceAccount %s", consentSweepSA)
	}

	for _, name := range []string{managerRole, managerSecretsRole} {
		for tr := range rbacTriples(allRules(find(docs, kindClusterRole, name))) {
			reads := tr.verb == verbGetController || tr.verb == verbList || tr.verb == verbWatch
			if tr.group == ichorGroup && tr.resource == resClusters && !reads {
				t.Errorf("%s grants %s: the controller writes no TalosCluster, it impersonates %s", name, tr, consentSweepSA)
			}
		}
	}

	imp := find(docs, kindRole, "ichor-consent-sweep-impersonate-role")
	if imp == nil {
		t.Fatal("no Role ichor-consent-sweep-impersonate-role")
	}

	want := rbacTriple{"", resServiceAccounts, verbImpersonate, consentSweepSA}
	if got := rbacTriples(allRules(imp)); len(got) != 1 || !got[want] {
		t.Errorf("the impersonation Role grants %v, want impersonate on %s only", got, consentSweepSA)
	}

	role := find(docs, kindClusterRole, "ichor-consent-sweep-role")
	if got := rbacTriples(allRules(role)); len(got) != 1 || !got[rbacTriple{ichorGroup, resClusters, verbPatch, ""}] {
		t.Errorf("ichor-consent-sweep-role grants %v, want patch on talosclusters only", got)
	}

	configDocs := loadYAMLDocs(t, consentSweepConfigDir+"role.yaml")
	const chartFile = "dist/chart/templates/rbac/ichor-consent-sweep.yaml"

	checkRBACRulesMatch(t, "ichor", "ClusterRole consent-sweep-role",
		find(configDocs, kindClusterRole, "consent-sweep-role"), role, consentSweepConfigDir+"role.yaml", chartFile)

	impDocs := loadYAMLDocs(t, consentSweepConfigDir+"impersonate_role.yaml")
	impFile := consentSweepConfigDir + "impersonate_role.yaml"
	checkRBACRulesMatch(t, "ichor", "Role consent-sweep-impersonate-role",
		find(impDocs, kindRole, "consent-sweep-impersonate-role"), imp, impFile, chartFile)
}
