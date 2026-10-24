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

// Package chart guards the hand-edits in dist/chart/templates/ that
// "kubebuilder edit --plugins=helm/v2-alpha" cannot regenerate on its own
// (see CONTRIBUTING.md and docs/adr/0006-webhook-serving-certificates.md).
// Regenerating the chart reverts those edits with no other CI signal: the
// chart workflow only runs "helm lint" and a deploy, neither of which sends
// an admission request. This test renders the chart with "helm template"
// (no cluster needed) and checks the invariants the edits encode.
package chart

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"testing"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const adrDoc = "docs/adr/0006-webhook-serving-certificates.md"

const (
	kindService                        = "Service"
	kindSecret                         = "Secret"
	kindRole                           = "Role"
	kindRoleBinding                    = "RoleBinding"
	kindValidatingWebhookConfiguration = "ValidatingWebhookConfiguration"
)

const (
	webhookServiceName = "ichor-webhook-service"
	webhookConfigName  = "ichor-validating-webhook-configuration"
	certSecretName     = "ichor-webhook-server-cert"
)

// TestChartInvariants renders the committed chart under both the
// conventional release name (Makefile's HELM_RELEASE) and a non-default one,
// and checks every invariant the hand-edits in templates/ exist to hold.
func TestChartInvariants(t *testing.T) {
	for _, release := range []string{"ichor", "something-else"} {
		t.Run(release, func(t *testing.T) {
			docs := renderChart(t, release)
			checkFixedNames(t, release, docs)
			checkWebhookInvariants(t, release, docs)
			checkNoCABundle(t, release, docs)
			checkCertSecretHasNoData(t, release, docs)
			checkSecretsRoleAndBinding(t, release, docs)
		})
	}
}

// renderChart runs "helm template" against the chart in this directory and
// decodes every rendered object into a generic map.
func renderChart(t *testing.T, release string) []map[string]any {
	t.Helper()

	out, err := exec.Command("helm", "template", release, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template %s .: %v\n%s", release, err, out)
	}

	var docs []map[string]any
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding helm template %s . output: %v", release, err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		t.Fatalf("helm template %s . produced no documents", release)
	}
	return docs
}

func kindOf(doc map[string]any) string {
	k, _ := doc["kind"].(string)
	return k
}

func nameOf(doc map[string]any) string {
	md, _ := doc["metadata"].(map[string]any)
	n, _ := md["name"].(string)
	return n
}

func findAll(docs []map[string]any, kind string) []map[string]any {
	out := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		if kindOf(d) == kind {
			out = append(out, d)
		}
	}
	return out
}

func find(docs []map[string]any, kind, name string) map[string]any {
	for _, d := range findAll(docs, kind) {
		if nameOf(d) == name {
			return d
		}
	}
	return nil
}

// containsStr reports whether v, a decoded YAML sequence, contains want.
func containsStr(v any, want string) bool {
	list, _ := v.([]any)
	for _, item := range list {
		if s, _ := item.(string); s == want {
			return true
		}
	}
	return false
}

// checkFixedNames guards the three resource names cmd/main.go's cert rotator
// hardcodes: they must survive unchanged under any release name.
func checkFixedNames(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	fixed := []struct{ kind, name string }{
		{kindService, webhookServiceName},
		{kindValidatingWebhookConfiguration, webhookConfigName},
		{kindSecret, certSecretName},
	}
	for _, f := range fixed {
		if find(docs, f.kind, f.name) == nil {
			t.Errorf(
				"helm template %s .: expected a %s named %q (fixed, not release-templated, "+
					"because cmd/main.go's cert rotator hardcodes it; see %s); not found",
				release, f.kind, f.name, adrDoc,
			)
		}
	}
}

// checkWebhookInvariants guards ADR 0006's fail-closed decision: both
// webhooks must keep failurePolicy: Fail and match DELETE only.
func checkWebhookInvariants(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	wh := find(docs, kindValidatingWebhookConfiguration, webhookConfigName)
	if wh == nil {
		t.Fatalf(
			"helm template %s .: expected a %s named %q to check failurePolicy/operations against; see %s",
			release, kindValidatingWebhookConfiguration, webhookConfigName, adrDoc,
		)
	}

	webhooks, _ := wh["webhooks"].([]any)
	if len(webhooks) == 0 {
		t.Fatalf("helm template %s .: expected %q to carry at least one webhook entry; see %s",
			release, webhookConfigName, adrDoc)
	}

	for _, w := range webhooks {
		wm, _ := w.(map[string]any)
		name, _ := wm["name"].(string)

		if fp, _ := wm["failurePolicy"].(string); fp != "Fail" {
			t.Errorf(
				"webhook %q: expected failurePolicy: Fail (ADR 0006 fails closed so a down webhook "+
					"blocks Ichor CR deletes instead of letting them through; see %s); got %q",
				name, adrDoc, fp,
			)
		}

		rules, _ := wm["rules"].([]any)
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			ops, _ := rm["operations"].([]any)
			if len(ops) != 1 || ops[0] != "DELETE" {
				t.Errorf("webhook %q: expected operations: [DELETE] only; see %s; got %v",
					name, adrDoc, ops)
			}
		}
	}
}

// checkNoCABundle guards the other half of ADR 0006: no caBundle may be
// templated anywhere, or it would drift on every Argo CD sync.
func checkNoCABundle(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	for _, d := range docs {
		if walkHasKey(d, "caBundle") {
			t.Errorf(
				"helm template %s .: found a caBundle key in %s %q; ADR 0006 ships the webhook "+
					"config with no caBundle (cert-controller injects it at runtime) so a templated "+
					"value here would drift on every Argo CD sync; see %s",
				release, kindOf(d), nameOf(d), adrDoc,
			)
		}
	}
}

func walkHasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x[key]; ok {
			return true
		}
		for _, vv := range x {
			if walkHasKey(vv, key) {
				return true
			}
		}
	case []any:
		for _, vv := range x {
			if walkHasKey(vv, key) {
				return true
			}
		}
	}
	return false
}

// checkCertSecretHasNoData guards the easy-to-get-backwards half of ADR
// 0006's Secret: it must exist, and it must carry no data key, so
// cert-controller's Get succeeds and its own Update is what fills it in.
func checkCertSecretHasNoData(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	secret := find(docs, kindSecret, certSecretName)
	if secret == nil {
		t.Fatalf("helm template %s .: expected a %s named %q; see %s",
			release, kindSecret, certSecretName, adrDoc)
	}
	if data, ok := secret["data"]; ok {
		t.Errorf(
			"helm template %s .: %q has a data key; ADR 0006 requires it ship empty "+
				"(metadata only) so cert-controller's Get succeeds and its own Update fills it "+
				"in at runtime; see %s; got data=%v",
			release, certSecretName, adrDoc, data,
		)
	}
}

// checkSecretsRoleAndBinding guards the RBAC half of the two findings: the
// Helm plugin can only emit one kind for the name controller-gen gives both
// the ClusterRole and the Role it calls "manager-role", so the Secret rules
// need their own Role, hand-created because the plugin silently drops them.
func checkSecretsRoleAndBinding(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	role := findSecretsRole(docs)
	if role == nil {
		t.Fatalf(
			"helm template %s .: expected a Role carrying get/patch/update on %q (restricted by "+
				"resourceNames) plus create/list/watch on secrets; the Helm plugin can only emit "+
				"one kind for controller-gen's shared \"manager-role\" name and silently drops this "+
				"rule set; see %s",
			release, certSecretName, adrDoc,
		)
	}

	roleName := nameOf(role)
	binding := findRoleBindingTo(docs, roleName)
	if binding == nil {
		t.Fatalf("helm template %s .: expected a %s pointing at Role %q; none found; see %s",
			release, kindRoleBinding, roleName, adrDoc)
	}
	if find(docs, kindRole, roleName) == nil {
		t.Errorf(
			"helm template %s .: %s %q references Role %q, but no %s with that name was "+
				"rendered; see %s",
			release, kindRoleBinding, nameOf(binding), roleName, kindRole, adrDoc,
		)
	}
}

func findSecretsRole(docs []map[string]any) map[string]any {
	for _, role := range findAll(docs, kindRole) {
		rules, _ := role["rules"].([]any)
		if hasCreateListWatchSecrets(rules) && hasCertResourceNameRule(rules) {
			return role
		}
	}
	return nil
}

func hasCreateListWatchSecrets(rules []any) bool {
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if _, hasNames := rm["resourceNames"]; hasNames {
			continue
		}
		if containsStr(rm["resources"], "secrets") &&
			containsStr(rm["verbs"], "create") &&
			containsStr(rm["verbs"], "list") &&
			containsStr(rm["verbs"], "watch") {
			return true
		}
	}
	return false
}

func hasCertResourceNameRule(rules []any) bool {
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		if !containsStr(rm["resourceNames"], certSecretName) {
			continue
		}
		if containsStr(rm["verbs"], "get") &&
			containsStr(rm["verbs"], "patch") &&
			containsStr(rm["verbs"], "update") {
			return true
		}
	}
	return false
}

func findRoleBindingTo(docs []map[string]any, roleName string) map[string]any {
	for _, rb := range findAll(docs, kindRoleBinding) {
		ref, _ := rb["roleRef"].(map[string]any)
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		if kind == kindRole && name == roleName {
			return rb
		}
	}
	return nil
}
