package chart

import (
	"slices"
	"testing"
)

const bundleRoleName = "ichor-bundle-secret-role"

// TestBundleSecretRoleGrantsOnlyTheNamedSecrets pins the role the four CLI commands that read the
// secrets-bundle Secret need: get on exactly the named Secrets, in the release namespace, nothing else.
func TestBundleSecretRoleGrantsOnlyTheNamedSecrets(t *testing.T) {
	off := renderChartWithValues(t, "ichor")
	if find(off, kindRole, bundleRoleName) != nil {
		t.Fatalf("Role %q rendered with no names; an empty resourceNames would grant every Secret", bundleRoleName)
	}

	docs := renderChartWithValues(t, "ichor", "rbac.bundleSecret.names={prod-secrets,dev-secrets}")

	role := find(docs, kindRole, bundleRoleName)
	if role == nil {
		t.Fatalf("Role %q is not rendered", bundleRoleName)
	}

	if ns := role["metadata"].(map[string]any)["namespace"]; ns != "default" {
		t.Errorf("namespace = %v, want the release namespace", ns)
	}

	rules := allRules(role)
	if len(rules) != 1 {
		t.Fatalf("%d rules, want exactly 1: %v", len(rules), rules)
	}

	rm, _ := rules[0].(map[string]any)

	if got := stringsOf(rm["apiGroups"]); !slices.Equal(got, []string{""}) {
		t.Errorf("apiGroups = %q, want the core group only", got)
	}

	if got := stringsOf(rm["resources"]); !slices.Equal(got, []string{"secrets"}) {
		t.Errorf("resources = %q, want secrets only", got)
	}

	if got := stringsOf(rm["verbs"]); !slices.Equal(got, []string{"get"}) {
		t.Errorf("verbs = %q, want get only", got)
	}

	if got := stringsOf(rm["resourceNames"]); !slices.Equal(got, []string{"prod-secrets", "dev-secrets"}) {
		t.Errorf("resourceNames = %q, want the two named Secrets", got)
	}

	if find(docs, kindClusterRole, bundleRoleName) != nil {
		t.Errorf("%q must be a namespaced Role, not a ClusterRole", bundleRoleName)
	}
}
