package chart

import (
	"strings"
	"testing"
)

var fixedIdentities = []string{"ichor-auto-patch", "ichor-storage-extensions", "ichor-consent-sweep"}

// TestNoWebhookNoImpersonatedIdentities: the identities' limits live in the validating webhook, so
// without it they must not exist, and no binding may name them.
func TestNoWebhookNoImpersonatedIdentities(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "webhook.enabled=false")

	for _, id := range fixedIdentities {
		if find(docs, kindServiceAccount, id) != nil {
			t.Errorf("ServiceAccount %s renders with webhook.enabled=false", id)
		}

		if find(docs, kindRole, id+"-impersonate-role") != nil {
			t.Errorf("Role %s-impersonate-role renders with webhook.enabled=false", id)
		}

		if find(docs, kindClusterRole, id+"-role") != nil {
			t.Errorf("ClusterRole %s-role renders with webhook.enabled=false", id)
		}
	}

	bindings := append(findAll(docs, kindRoleBinding), findAll(docs, kindClusterRoleBinding)...)
	for _, b := range bindings {
		subjects, _ := b["subjects"].([]any)
		for _, s := range subjects {
			sm, _ := s.(map[string]any)
			for _, id := range fixedIdentities {
				if sm["name"] == id {
					t.Errorf("%s %s has subject %s with webhook.enabled=false", b["kind"], nameOf(b), id)
				}
			}
		}

		if ref, _ := b["roleRef"].(map[string]any); ref != nil {
			for _, id := range fixedIdentities {
				if strings.HasPrefix(ref["name"].(string), id) {
					t.Errorf("%s %s refers to %v with webhook.enabled=false", b["kind"], nameOf(b), ref["name"])
				}
			}
		}
	}
}

func TestNamespacedRBACIsRefused(t *testing.T) {
	out, err := renderChartWithValuesErr("ichor", "rbac.namespaced=true")
	if err == nil {
		t.Fatal("rbac.namespaced=true rendered; want it refused")
	}

	if !strings.Contains(string(out), "not supported") {
		t.Errorf("error does not say \"not supported\": %s", out)
	}
}

func TestBringYourOwnServiceAccount(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "serviceAccount.enabled=false", "serviceAccount.name=byo")

	if got := deploymentServiceAccountName(findNameSuffix(docs, kindDeployment, "controller-manager")); got != "byo" {
		t.Errorf("Deployment serviceAccountName = %q, want byo", got)
	}

	for _, sa := range findAll(docs, kindServiceAccount) {
		if nameOf(sa) == "ichor-controller-manager" || nameOf(sa) == "byo" {
			t.Errorf("ServiceAccount %s renders with serviceAccount.enabled=false", nameOf(sa))
		}
	}

	found := false
	bindings := append(findAll(docs, kindRoleBinding), findAll(docs, kindClusterRoleBinding)...)
	for _, b := range bindings {
		subjects, _ := b["subjects"].([]any)
		for _, s := range subjects {
			sm, _ := s.(map[string]any)
			if sm["kind"] != kindServiceAccount {
				continue
			}

			name, _ := sm["name"].(string)
			if strings.HasPrefix(name, "ichor-controller-manager") {
				t.Errorf("%s %s binds %s, not byo", b["kind"], nameOf(b), name)
			}

			if name == "byo" {
				found = true
			}
		}
	}

	if !found {
		t.Error("no binding names byo")
	}

}
