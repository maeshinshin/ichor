package chart

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Value sets the chart tests share, named once so that none of them is a literal in many files.
const (
	oidcEnabledSet = "ichorUI.oidc.enabled=true"
	oidcClientID   = "ichorUI.oidc.clientId=ichor"
	oidcIssuerSet  = "ichorUI.oidc.issuer=https://idp.example.test"
	installerOn    = "installer.enabled=true"
	uiOffSet       = "ichorUI.enabled=false"
	usersURLShort  = "ichorUI.users.externalURL=https://x"
	impUsersSet    = "ichorUI.oidc.impersonate.users={alice}"
	impGroupsSet   = "ichorUI.oidc.impersonate.groups={dev}"
	allowOpenSet   = "ichorUI.allowUnauthenticated=true"
)

// destructiveStarts are the six subresources of talosoperations whose `start` verb starts an operation
// that can take a cluster down. They are written out here, the way the CRD and docs/concepts/security.md
// name them, and not read from the role under test or from StartSubresource, which is what the backend
// asks. TestDestructiveStartsAreTheSixTheCRDHas ties the list to the CRD, so a seventh cannot slip past.
var destructiveStarts = []string{
	"shutdown", "ca-rotation", "key-rotation", "etcd-restore", "node-reset", "node-shutdown",
}

// ruleGrants reports whether rm, a decoded RBAC rule, gives verb on resource in the ichor group, the
// wildcards the API server honours included.
func ruleGrants(rm map[string]any, verb, resource string) bool {
	if !containsStr(rm["apiGroups"], ichorGroup) && !containsStr(rm["apiGroups"], "*") {
		return false
	}

	if !containsStr(rm["verbs"], verb) && !containsStr(rm["verbs"], "*") {
		return false
	}

	base, _, _ := strings.Cut(resource, "/")

	for _, r := range stringsOf(rm["resources"]) {
		if r == "*" || r == resource || r == base+"/*" || strings.HasPrefix(r, "*/") && strings.HasSuffix(resource, r[1:]) {
			return true
		}
	}

	return false
}

// serviceAccountRules returns the rules every Role and ClusterRole bound, in docs, to the ServiceAccount
// name gets, through any RoleBinding or ClusterRoleBinding.
func serviceAccountRules(docs []map[string]any, name string) []map[string]any {
	var out []map[string]any

	for _, kind := range []string{kindClusterRoleBinding, kindRoleBinding} {
		for _, b := range findAll(docs, kind) {
			subjects, _ := b["subjects"].([]any)

			bound := slices.ContainsFunc(subjects, func(s any) bool {
				sm, _ := s.(map[string]any)

				return kindOf(sm) == kindServiceAccount && sm["name"] == name
			})
			if !bound {
				continue
			}

			ref, _ := b["roleRef"].(map[string]any)
			refName, _ := ref["name"].(string)

			for _, rule := range allRules(find(docs, kindOf(ref), refName)) {
				rm, _ := rule.(map[string]any)
				out = append(out, rm)
			}
		}
	}

	return out
}

// TestUIServiceAccountHoldsNoStartVerb pins that ichor-ui's ServiceAccount cannot start a destructive
// operation, under every value set that changes ichor-ui's RBAC. It is the caller of nothing: a write is
// made as the signed-in user, and the validating webhook asks Kubernetes about whoever wrote the
// TalosOperation, so a start verb here would pass a start nobody signed in for.
func TestUIServiceAccountHoldsNoStartVerb(t *testing.T) {
	for _, sets := range [][]string{
		nil,
		{usersOn, usersURL},
		{
			oidcEnabledSet, oidcIssuerSet, oidcClientID,
			impGroupsSet, impUsersSet,
		},
		{"ichorUI.credentials.serviceAccount=true"},
		{"ichorUI.diagnostics.serviceAccount=true"},
		{installerOn},
	} {
		docs := renderChartWithValues(t, "ichor", sets...)

		rules := serviceAccountRules(docs, ichorUIServiceAccountName)
		if len(rules) == 0 {
			t.Fatalf("%v: no rule is bound to %s: the test would pass for any role", sets, ichorUIServiceAccountName)
		}

		for _, sub := range destructiveStarts {
			for _, rm := range rules {
				if ruleGrants(rm, verbStart, "talosoperations/"+sub) {
					t.Errorf("%v: %s holds start on talosoperations/%s through %v", sets, ichorUIServiceAccountName, sub, rm)
				}
			}
		}
	}

	// A control that the matcher sees a grant: a wildcard binding of start to a ServiceAccount.
	control := decodeYAMLDocs(t, []byte(wildcardGrant), "the control")
	got := serviceAccountRules(control, "x")
	if len(got) != 1 || !ruleGrants(got[0], verbStart, "talosoperations/shutdown") {
		t.Errorf("the matcher does not see a wildcard grant of start: %v", got)
	}

	// RemoveMachine is the only delete of a TalosMachine and asks for the override verb, which the
	// ServiceAccount does not hold, so no mode reaches it as the ServiceAccount.
	for _, rm := range serviceAccountRules(renderChartWithValues(t, "ichor"), ichorUIServiceAccountName) {
		if ruleGrants(rm, verbDelete, "talosmachines") {
			t.Errorf("the ServiceAccount holds delete on talosmachines: %v", rm)
		}
	}
}

// TestDestructiveStartsAreTheSixTheCRDHas ties destructiveStarts to the CRD, the record the generator
// writes from the API types: every kind and node action the CRD accepts that StartSubresource gives a
// subresource must be one of the six, and every one of the six must be reachable.
func TestDestructiveStartsAreTheSixTheCRDHas(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/ichor.maesh.dev_talosoperations.yaml")
	if err != nil {
		t.Fatal(err)
	}

	var crd map[string]any
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}

	spec := walk(t, crd, "spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec", "properties")
	kinds := stringsOf(walk(t, spec, "kind", "enum"))
	actions := stringsOf(walk(t, spec, "node", "properties", "action", "enum"))

	var reached []string

	for _, kind := range kinds {
		op := ichorv1alpha1.TalosOperationSpec{Kind: kind}

		if kind == ichorv1alpha1.TalosOperationKindNode {
			for _, action := range actions {
				op.Node = &ichorv1alpha1.TalosOperationNodeSpec{Action: action}
				reached = append(reached, op.StartSubresource())
			}

			continue
		}

		reached = append(reached, op.StartSubresource())
	}

	reached = slices.DeleteFunc(reached, func(s string) bool { return s == "" })
	slices.Sort(reached)

	want := slices.Clone(destructiveStarts)
	slices.Sort(want)

	if !slices.Equal(slices.Compact(reached), want) {
		t.Errorf("the CRD's kinds %v and node actions %v need the start subresources %v, destructiveStarts lists %v: "+
			"a new destructive kind must be added to the list, so that the ServiceAccount test covers it",
			kinds, actions, slices.Compact(reached), want)
	}
}

// walk descends through decoded YAML by map key or list index (a decimal string).
func walk(t *testing.T, v any, path ...string) any {
	t.Helper()

	for _, p := range path {
		switch x := v.(type) {
		case map[string]any:
			v = x[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i >= len(x) {
				t.Fatalf("no element %q", p)
			}

			v = x[i]
		default:
			t.Fatalf("cannot descend into %T at %q", v, p)
		}
	}

	return v
}

const wildcardGrant = `
kind: ClusterRoleBinding
subjects: [{kind: ServiceAccount, name: x}]
roleRef: {kind: ClusterRole, name: r}
---
kind: ClusterRole
metadata: {name: r}
rules:
- {apiGroups: [ichor.maesh.dev], resources: ["talosoperations/*"], verbs: ["*"]}
`
