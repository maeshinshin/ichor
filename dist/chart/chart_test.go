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
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const adrDoc = "docs/adr/0006-webhook-serving-certificates.md"

// adr0013Doc is ADR 0013 (CNI presets), which covers why the
// kube-proxy-switch guard is a ValidatingAdmissionPolicy and why it must
// ship in this chart, not only in config/admission.
const adr0013Doc = "docs/adr/0013-cni-presets.md"

// crdSourceDir is config/crd/bases, the controller-gen output dist/chart's
// CRD templates are generated from (CONTRIBUTING.md's chart warning).
const crdSourceDir = "../../config/crd/bases"

const (
	kindService                        = "Service"
	kindSecret                         = "Secret"
	kindServiceAccount                 = "ServiceAccount"
	kindRole                           = "Role"
	kindRoleBinding                    = "RoleBinding"
	kindClusterRole                    = "ClusterRole"
	kindClusterRoleBinding             = "ClusterRoleBinding"
	kindDeployment                     = "Deployment"
	kindValidatingWebhookConfiguration = "ValidatingWebhookConfiguration"
	kindCustomResourceDefinition       = "CustomResourceDefinition"
	kindValidatingAdmissionPolicy      = "ValidatingAdmissionPolicy"
	kindValidatingAdmissionPolicyBind  = "ValidatingAdmissionPolicyBinding"
)

const (
	webhookServiceName = "ichor-webhook-service"
	webhookConfigName  = "ichor-validating-webhook-configuration"
	certSecretName     = "ichor-webhook-server-cert"

	kubeProxySwitchPolicyName  = "ichor-cni-kubeproxy-switch"
	kubeProxySwitchBindingName = "ichor-cni-kubeproxy-switch-binding"

	// ichor-ui's own RBAC (ADR 0018, ADR 0020): fixed names, mirroring
	// config/rbac/ichor-ui/, unlike the controller's own release-templated ones.
	ichorUIServiceAccountName = "ichor-ui"
	ichorUIRoleName           = "ichor-ui-role"
	ichorUIRoleBindingName    = "ichor-ui-rolebinding"
	ichorUIDeploymentName     = "ichor-ui"
)

// adr0020Doc is ADR 0020 (giving ichor-ui a shipping path): the controller and the UI
// backend must keep separate Kubernetes identities (architecture.md "Trust").
const adr0020Doc = "docs/adr/0020-ichor-ui-shipping-path.md"

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
			checkNoClusterWideSecretsRead(t, release, docs)
			checkCRDsMatchConfig(t, release, docs)
			checkKubeProxySwitchPolicy(t, release, docs)
			checkUIBackendRBACIsSeparate(t, release, docs)
		})
	}
}

// renderChart runs "helm template" against the chart in this directory and
// decodes every rendered object into a generic map.
func renderChart(t *testing.T, release string) []map[string]any {
	t.Helper()
	return renderChartWithValues(t, release)
}

// renderChartWithValues is renderChart plus --set overrides, for invariants that only apply
// once a feature flag is on: certManager.enabled's metrics Secret and the ServiceMonitor TLS
// config referencing it don't exist at all under the suite's default (flags-off) render.
func renderChartWithValues(t *testing.T, release string, sets ...string) []map[string]any {
	t.Helper()

	args := make([]string, 0, 3+2*len(sets))
	args = append(args, "template", release, ".")
	for _, s := range sets {
		args = append(args, "--set", s)
	}

	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	var docs []map[string]any
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decoding helm %s output: %v", strings.Join(args, " "), err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
	if len(docs) == 0 {
		t.Fatalf("helm %s produced no documents", strings.Join(args, " "))
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

// checkKubeProxySwitchPolicy guards ADR 0013's kube-proxy-switch guard: the
// chart must install both the ValidatingAdmissionPolicy and its Binding --
// a Policy without a Binding enforces nothing, and a Binding naming a
// Policy that doesn't exist is equally inert, so neither half alone is a
// working guard. It also checks the Binding actually points at the Policy
// this chart ships, not a stale or mistyped name.
func checkKubeProxySwitchPolicy(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	policy := find(docs, kindValidatingAdmissionPolicy, kubeProxySwitchPolicyName)
	if policy == nil {
		t.Errorf(
			"helm template %s .: expected a %s named %q (kube-proxy-switch guard; see %s); not found",
			release, kindValidatingAdmissionPolicy, kubeProxySwitchPolicyName, adr0013Doc,
		)
	}

	binding := find(docs, kindValidatingAdmissionPolicyBind, kubeProxySwitchBindingName)
	if binding == nil {
		t.Errorf(
			"helm template %s .: expected a %s named %q (kube-proxy-switch guard; see %s); not found",
			release, kindValidatingAdmissionPolicyBind, kubeProxySwitchBindingName, adr0013Doc,
		)
	}

	if binding != nil {
		spec, _ := binding["spec"].(map[string]any)
		policyName, _ := spec["policyName"].(string)
		if policyName != kubeProxySwitchPolicyName {
			t.Errorf(
				"helm template %s .: %s %q has spec.policyName %q, want %q (see %s); "+
					"a binding to the wrong policy name enforces nothing",
				release, kindValidatingAdmissionPolicyBind, kubeProxySwitchBindingName, policyName,
				kubeProxySwitchPolicyName, adr0013Doc,
			)
		}
	}
}

// checkUIBackendRBACIsSeparate guards architecture.md "Trust": "only the controller
// holds an os:admin-equivalent Talos identity[;] the UI backend holds a read-only one."
// Collapsing the two -- the UI Deployment running as the controller's own
// ServiceAccount, or the UI's ClusterRole merged into manager-role -- would erase that
// boundary with no other signal, since both would still "work". Checks the whole chain:
// ichor-ui's Deployment runs as its own ServiceAccount, that ServiceAccount is bound only
// to ichor-ui's own ClusterRole, and at least one other ServiceAccount/ClusterRoleBinding
// pair exists (the controller's) that names neither.
func checkUIBackendRBACIsSeparate(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	uiDeployment := find(docs, kindDeployment, ichorUIDeploymentName)
	if uiDeployment == nil {
		t.Fatalf("helm template %s .: expected a %s named %q; see %s",
			release, kindDeployment, ichorUIDeploymentName, adr0020Doc)
	}
	if sa := deploymentServiceAccountName(uiDeployment); sa != ichorUIServiceAccountName {
		t.Errorf(
			"helm template %s .: %s %q runs as ServiceAccount %q, want %q (its own identity, "+
				"separate from the controller's; see %s)",
			release, kindDeployment, ichorUIDeploymentName, sa, ichorUIServiceAccountName, adr0020Doc,
		)
	}

	if find(docs, kindServiceAccount, ichorUIServiceAccountName) == nil {
		t.Fatalf("helm template %s .: expected a %s named %q; see %s",
			release, kindServiceAccount, ichorUIServiceAccountName, adr0020Doc)
	}
	if find(docs, kindClusterRole, ichorUIRoleName) == nil {
		t.Fatalf("helm template %s .: expected a %s named %q; see %s",
			release, kindClusterRole, ichorUIRoleName, adr0020Doc)
	}

	binding := find(docs, kindClusterRoleBinding, ichorUIRoleBindingName)
	if binding == nil {
		t.Fatalf("helm template %s .: expected a %s named %q; see %s",
			release, kindClusterRoleBinding, ichorUIRoleBindingName, adr0020Doc)
	}
	roleRef, subject := clusterRoleBindingRefs(binding)
	if roleRef != ichorUIRoleName || subject != ichorUIServiceAccountName {
		t.Errorf(
			"helm template %s .: %s %q binds ClusterRole %q to ServiceAccount %q, want %q to %q; see %s",
			release, kindClusterRoleBinding, ichorUIRoleBindingName, roleRef, subject,
			ichorUIRoleName, ichorUIServiceAccountName, adr0020Doc,
		)
	}

	// At least one other ClusterRoleBinding must exist (the controller's own) whose
	// ClusterRole and ServiceAccount are neither of ichor-ui's -- otherwise the two
	// identities could have silently become the same one.
	foundDistinctOther := false
	for _, b := range findAll(docs, kindClusterRoleBinding) {
		if nameOf(b) == ichorUIRoleBindingName {
			continue
		}
		roleRef, subject := clusterRoleBindingRefs(b)
		if roleRef != ichorUIRoleName && subject != ichorUIServiceAccountName {
			foundDistinctOther = true
			break
		}
	}
	if !foundDistinctOther {
		t.Errorf(
			"helm template %s .: expected at least one other %s (the controller's own) binding "+
				"neither ClusterRole %q nor ServiceAccount %q; the controller and the UI backend "+
				"must keep separate identities; see %s",
			release, kindClusterRoleBinding, ichorUIRoleName, ichorUIServiceAccountName, adr0020Doc,
		)
	}
}

// deploymentServiceAccountName reads spec.template.spec.serviceAccountName from a decoded
// Deployment.
func deploymentServiceAccountName(deployment map[string]any) string {
	spec, _ := deployment["spec"].(map[string]any)
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)
	sa, _ := podSpec["serviceAccountName"].(string)
	return sa
}

// clusterRoleBindingRefs reads a decoded ClusterRoleBinding's roleRef.name and its first
// subject's name.
func clusterRoleBindingRefs(binding map[string]any) (roleRef, subject string) {
	ref, _ := binding["roleRef"].(map[string]any)
	roleRef, _ = ref["name"].(string)

	subjects, _ := binding["subjects"].([]any)
	if len(subjects) > 0 {
		sm, _ := subjects[0].(map[string]any)
		subject, _ = sm["name"].(string)
	}
	return roleRef, subject
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

// checkNoClusterWideSecretsRead guards the regression ADR 0011 records
// (commit 645f5be reintroduced a ClusterRole rule granting unrestricted
// get/list/watch on every Secret in every namespace, which 823b04f had
// already removed once): no ClusterRole in the rendered chart may grant
// get, list or watch on secrets without resourceNames to restrict it. The
// toggled manager-role template renders as a Role instead of a ClusterRole
// under rbac.namespaced: true, but the default (false, checked here) is
// exactly the case that regressed.
func checkNoClusterWideSecretsRead(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	for _, cr := range findAll(docs, kindClusterRole) {
		rules, _ := cr["rules"].([]any)
		for _, r := range rules {
			rm, _ := r.(map[string]any)
			if _, hasNames := rm["resourceNames"]; hasNames {
				continue
			}

			if !containsStr(rm["resources"], "secrets") {
				continue
			}

			if containsStr(rm["verbs"], "get") || containsStr(rm["verbs"], "list") || containsStr(rm["verbs"], "watch") {
				t.Errorf(
					"helm template %s .: ClusterRole %q grants %v on secrets across every namespace with no "+
						"resourceNames; the bundle Secret is resolved in the controller's own namespace (ADR "+
						"0011), so this permission must live in a namespace-scoped Role instead",
					release, nameOf(cr), rm["verbs"],
				)
			}
		}
	}
}

// checkCRDsMatchConfig guards against the drift that shipped a
// TalosConfigPatch CRD chart users could not use typed configuration with:
// every CRD generated into config/crd/bases/ must appear in the rendered
// chart with an equivalent schema. It compares parsed objects, not raw text,
// so the chart's "{{- if .Values.crd.enabled }}" wrapper and reindentation
// never cause a false failure.
func checkCRDsMatchConfig(t *testing.T, release string, docs []map[string]any) {
	t.Helper()

	entries, err := os.ReadDir(crdSourceDir)
	if err != nil {
		t.Fatalf("reading %s: %v", crdSourceDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(crdSourceDir, entry.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		var want map[string]any
		if err := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(&want); err != nil {
			t.Fatalf("decoding %s: %v", path, err)
		}

		name := nameOf(want)
		got := find(docs, kindCustomResourceDefinition, name)
		if got == nil {
			t.Fatalf("%s", crdDriftMsg(release, name, path, "is missing from the chart entirely"))
			continue
		}

		for _, detail := range diffCRDSchemas(want, got) {
			t.Errorf("%s", crdDriftMsg(release, name, path, detail))
		}
	}
}

// crdDriftMsg builds the failure message checkCRDsMatchConfig reports: it
// names the CRD and the detail, says dist/chart is generated from config/
// and the fix is to re-sync it, and points at CONTRIBUTING.md's chart
// warning.
func crdDriftMsg(release, name, sourcePath, detail string) string {
	return "helm template " + release + " .: CustomResourceDefinition " + name + " " + detail +
		"; dist/chart is generated from config/ (see CONTRIBUTING.md's chart warning) " +
		"and has drifted out of sync with " + sourcePath + "; re-sync it"
}

// diffCRDSchemas compares, for each version, the full schema and (for a
// more specific message) the set of top-level spec properties keys. It
// returns one detail string per mismatch found, or nil if the CRDs agree.
func diffCRDSchemas(want, got map[string]any) []string {
	var details []string

	wantVersions := crdVersions(want)
	gotVersions := crdVersions(got)

	for name, wantVersion := range wantVersions {
		gotVersion, ok := gotVersions[name]
		if !ok {
			details = append(details, "is missing version "+name+" in the chart")
			continue
		}

		wantSchema, _ := wantVersion["schema"].(map[string]any)
		gotSchema, _ := gotVersion["schema"].(map[string]any)
		if reflect.DeepEqual(wantSchema, gotSchema) {
			continue
		}

		wantKeys := specPropertyKeys(wantSchema)
		gotKeys := specPropertyKeys(gotSchema)
		if !reflect.DeepEqual(wantKeys, gotKeys) {
			details = append(details, "version "+name+"'s spec properties are "+
				strings.Join(gotKeys, ",")+" in the chart but "+strings.Join(wantKeys, ",")+
				" in config/crd/bases")
			continue
		}

		details = append(details, "version "+name+"'s schema differs from config/crd/bases "+
			"even though its top-level spec properties match")
	}

	return details
}

// crdVersions indexes a decoded CRD's spec.versions by version name.
func crdVersions(doc map[string]any) map[string]map[string]any {
	spec, _ := doc["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)

	out := make(map[string]map[string]any, len(versions))
	for _, v := range versions {
		vm, _ := v.(map[string]any)
		name, _ := vm["name"].(string)
		out[name] = vm
	}
	return out
}

// metricsCertSecretName is cmd/main.go's second cert-controller rotator's fixed Secret name
// (ADR 0006, ADR 0031) -- extras/metrics-server-cert.yaml must ship it under this exact name,
// and the ServiceMonitor's tlsConfig must reference this exact name back, or Prometheus
// Operator resolves a certificate that was never created.
const metricsCertSecretName = "ichor-metrics-server-cert"

// TestMetricsCertManagerEnabled guards certManager.enabled=true, which renders nothing at all
// under TestChartInvariants's default-values render: the Secret cert-controller's second
// rotator writes into, and the ServiceMonitor's tlsConfig referencing it, must name the exact
// same Secret -- caught empirically once, by actually rendering with the flag on rather than
// reading the templates statically (the ServiceMonitor used to hardcode the unprefixed
// "metrics-server-cert" while the Secret shipped as "ichor-metrics-server-cert").
func TestMetricsCertManagerEnabled(t *testing.T) {
	docs := renderChartWithValues(t, "ichor", "certManager.enabled=true", "prometheus.enabled=true")

	secret := find(docs, kindSecret, metricsCertSecretName)
	if secret == nil {
		t.Fatalf("helm template . --set certManager.enabled=true: expected a %s named %q; see %s",
			kindSecret, metricsCertSecretName, adr0020Doc)
	}
	if data, ok := secret["data"]; ok {
		t.Errorf("helm template . --set certManager.enabled=true: %q has a data key; cert-controller's "+
			"rotator has no Create path, only Update, so this must ship empty; got data=%v",
			metricsCertSecretName, data)
	}

	monitor := find(docs, "ServiceMonitor", "ichor-controller-manager-metrics-monitor")
	if monitor == nil {
		t.Fatalf("helm template . --set certManager.enabled=true,prometheus.enabled=true: " +
			"expected a ServiceMonitor named \"ichor-controller-manager-metrics-monitor\"")
	}
	const nameKey = "name"
	tlsConfigPath := []string{"spec", "endpoints", "0", "tlsConfig"}
	for _, suffix := range [][]string{
		{"ca", "secret", nameKey},
		{"cert", "secret", nameKey},
		{"keySecret", nameKey},
	} {
		path := append(append([]string{}, tlsConfigPath...), suffix...)
		got := walkPath(monitor, path)
		if got != metricsCertSecretName {
			t.Errorf("ServiceMonitor %s: want %q, got %q; it must name the same Secret "+
				"extras/metrics-server-cert.yaml ships (%q)",
				strings.Join(path, "."), metricsCertSecretName, got, metricsCertSecretName)
		}
	}
}

// walkPath descends a decoded document through nested maps and, for a numeric segment, a
// slice index, returning "" if any step doesn't match.
func walkPath(doc map[string]any, path []string) string {
	var cur any = doc
	for _, p := range path {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(v) {
				return ""
			}
			cur = v[i]
		default:
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

// specPropertyKeys returns the sorted set of top-level spec.properties keys
// a CRD version's schema declares.
func specPropertyKeys(schema map[string]any) []string {
	openAPI, _ := schema["openAPIV3Schema"].(map[string]any)
	props, _ := openAPI["properties"].(map[string]any)
	specProp, _ := props["spec"].(map[string]any)
	specProps, _ := specProp["properties"].(map[string]any)

	keys := make([]string, 0, len(specProps))
	for k := range specProps {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
