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

package chart

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func toYAMLString(t *testing.T, v any) string {
	t.Helper()

	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// The CIDR values replace the `world` entity, and the default keeps it.
func TestCiliumCIDRValuesReplaceWorld(t *testing.T) {
	render := func(extra ...string) string {
		var b strings.Builder

		values := append(append([]string{}, netpolAllOn...), extra...)

		for _, d := range findAll(renderChartWithValues(t, "ichor", values...), kindCNP) {
			b.WriteString(nameOf(d) + "\n")
			spec, _ := d["spec"].(map[string]any)
			b.WriteString(toYAMLString(t, spec))
		}

		return b.String()
	}

	def := render()
	if !strings.Contains(def, "world") || strings.Contains(def, "toCIDR") || strings.Contains(def, "fromCIDR") {
		t.Fatalf("the default policies must allow world and name no CIDR:\n%s", def)
	}

	narrow := render(
		"networkPolicy.cilium.externalCIDRs={10.0.0.0/8}",
		"networkPolicy.cilium.talosCIDRs={192.0.2.0/24}",
		"networkPolicy.cilium.uiIngressCIDRs={198.51.100.0/24}",
	)
	for _, want := range []string{"10.0.0.0/8", "192.0.2.0/24", "198.51.100.0/24"} {
		if !strings.Contains(narrow, want) {
			t.Fatalf("the narrowed policies lack %s:\n%s", want, narrow)
		}
	}

	if strings.Contains(narrow, "world") {
		t.Fatalf("the narrowed policies still allow world:\n%s", narrow)
	}
}

// Both pods run as the image's user under the PriorityClass the chart creates.
func TestPodsRunAsTheImageUserUnderThePriorityClass(t *testing.T) {
	docs := renderChart(t, "ichor")

	if len(findAll(docs, "PriorityClass")) != 1 {
		t.Fatal("the chart does not render exactly one PriorityClass")
	}

	deployments := findAll(docs, "Deployment")
	if len(deployments) < 2 {
		t.Fatalf("want the controller and ichor-ui Deployments, got %d", len(deployments))
	}

	for _, d := range deployments {
		out := toYAMLString(t, d)
		for _, want := range []string{"priorityClassName: ichor-critical", "runAsUser: 65532", "runAsGroup: 65532"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s lacks %q", nameOf(d), want)
			}
		}
	}
}

func policyValidations(policy map[string]any) (exprs []string, messages []string) {
	spec, _ := policy["spec"].(map[string]any)
	list, _ := spec["validations"].([]any)

	for _, v := range list {
		vm, _ := v.(map[string]any)
		e, _ := vm["expression"].(string)
		m, _ := vm["message"].(string)
		exprs = append(exprs, strings.Join(strings.Fields(e), " "))
		messages = append(messages, m)
	}

	return exprs, messages
}

// TestControllerJobsPolicyMirrorsConfig guards: the chart ships the same Job policies as
// config/admission (the release namespace and the controller's ServiceAccount substituted), plus its
// own image-repository rule, and nothing renders with admission.enabled=false.
func TestControllerJobsPolicyMirrorsConfig(t *testing.T) {
	cfg := loadYAMLDocs(t, "../../config/admission/controller-jobs-policy.yaml")
	docs := renderChart(t, "ichor")

	for _, name := range []string{"ichor-controller-jobs", "ichor-controller-job-image"} {
		want := find(cfg, kindValidatingAdmissionPolicy, name)
		got := find(docs, kindValidatingAdmissionPolicy, name)

		if want == nil || got == nil {
			t.Fatalf("policy %s: config=%v chart=%v", name, want != nil, got != nil)
		}

		wantExprs, _ := policyValidations(want)
		gotExprs, gotMsgs := policyValidations(got)

		var kept []string

		for i, e := range gotExprs {
			if strings.Contains(gotMsgs[i], "admission.jobImageRepository") {
				continue
			}

			e = strings.ReplaceAll(e, "'default'", "'ichor-system'")
			kept = append(kept, strings.ReplaceAll(e, "system:serviceaccount:default:", "system:serviceaccount:ichor-system:"))
		}

		if strings.Join(kept, "\n") != strings.Join(wantExprs, "\n") {
			t.Errorf("policy %s: chart validations differ from config/admission/controller-jobs-policy.yaml\nchart:  %q\nconfig: %q", name, kept, wantExprs)
		}

		if find(docs, kindValidatingAdmissionPolicyBind, name+"-binding") == nil {
			t.Errorf("no binding for %s", name)
		}
	}

	off := renderChartWithValues(t, "ichor", "admission.enabled=false")
	if find(off, kindValidatingAdmissionPolicy, "ichor-controller-jobs") != nil {
		t.Error("the Job policy renders with admission.enabled=false")
	}

	noRepo := renderChartWithValues(t, "ichor", "admission.jobImageRepository=")
	_, msgs := policyValidations(find(noRepo, kindValidatingAdmissionPolicy, "ichor-controller-jobs"))

	for _, m := range msgs {
		if strings.Contains(m, "admission.jobImageRepository") {
			t.Error("the image-repository rule renders with an empty admission.jobImageRepository")
		}
	}
}

// TestControllerDeletesPodsOnlyInKubeSystem guards: the controller's cluster-wide rules
// grant no delete on pods (it evicts to drain); the one direct delete, the stale agent pod of an etcd
// restore, is a Role in kube-system, in config/rbac and in the chart.
func TestControllerDeletesPodsOnlyInKubeSystem(t *testing.T) {
	deletePods := rbacTriple{"", "pods", "delete", ""}

	chartDocs := renderChart(t, "ichor")
	cfgDocs := loadYAMLDocs(t, "../../config/rbac/pod_restart_role.yaml")

	for _, d := range findAll(chartDocs, kindClusterRole) {
		if strings.HasSuffix(nameOf(d), "-manager-role") && rbacTriples(allRules(d))[deletePods] {
			t.Errorf("chart ClusterRole %s grants %s cluster-wide", nameOf(d), deletePods)
		}
	}

	for _, d := range loadYAMLDocs(t, controllerConfigRolePath) {
		if kindOf(d) == kindClusterRole && rbacTriples(allRules(d))[deletePods] {
			t.Errorf("config/rbac/role.yaml ClusterRole %s grants %s cluster-wide", nameOf(d), deletePods)
		}
	}

	for label, docs := range map[string][]map[string]any{"chart": chartDocs, "config/rbac": cfgDocs} {
		var role map[string]any

		for _, d := range findAll(docs, kindRole) {
			if namespaceOf(d) == "kube-system" && rbacTriples(allRules(d))[deletePods] {
				role = d
			}
		}

		if role == nil {
			t.Errorf("%s: no Role in kube-system grants %s", label, deletePods)
			continue
		}

		var bound bool

		for _, b := range findAll(docs, kindRoleBinding) {
			ref, _ := b["roleRef"].(map[string]any)
			if namespaceOf(b) == "kube-system" && ref["name"] == nameOf(role) {
				bound = true
			}
		}

		if !bound {
			t.Errorf("%s: Role %s in kube-system has no RoleBinding", label, nameOf(role))
		}
	}
}
