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
)

const kubeSystemNs = "kube-system"

// checkCNIRoleMatchesConfig compares the chart's ichor-<cfgName> of kind with cfgName in config/rbac/ichor-cni/<cfgFile>
// (hand-written) as a rule set.
func checkCNIRoleMatchesConfig(t *testing.T, docs []map[string]any, kind, cfgName, cfgFile string) {
	t.Helper()

	checkRBACRulesMatch(t, "ichor", kind+" "+cfgName,
		find(loadYAMLDocs(t, "../../config/rbac/ichor-cni/"+cfgFile), kind, cfgName), find(docs, kind, "ichor-"+cfgName),
		"config/rbac/ichor-cni/"+cfgFile, "dist/chart/templates/rbac/ichor-cni.yaml")
}

// TestCNIHelmRBAC pins the Cilium Helm Job's identity: the ServiceAccount
// ichor-cni the controller names in the Job exists, its ClusterRole equals config/rbac/ichor-cni,
// it can be turned off, and it holds no wildcard.
func TestCNIHelmRBAC(t *testing.T) {
	t.Run("matches config", func(t *testing.T) {
		docs := renderChart(t, "ichor")

		if find(docs, kindServiceAccount, "ichor-cni") == nil {
			t.Fatalf("the chart renders no ServiceAccount ichor-cni, the name the controller puts in the Job")
		}

		clusterRole := find(docs, kindClusterRole, "ichor-cni-role")
		role := find(docs, kindRole, "ichor-cni-role")

		checkCNIRoleMatchesConfig(t, docs, kindClusterRole, "cni-role", "role.yaml")
		checkCNIRoleMatchesConfig(t, docs, kindRole, "cni-role", "role.yaml")

		// The takeover's Secret rights in kube-system: namespaced, get/list/delete only.
		records := find(docs, kindRole, "ichor-cni-records-role")
		if records == nil || namespaceOf(records) != kubeSystemNs {
			t.Fatalf("Role ichor-cni-records-role = %v, want it in kube-system", records)
		}

		checkCNIRoleMatchesConfig(t, docs, kindRole, "cni-records-role", "records_role.yaml")

		for triple := range rbacTriples(append(allRules(clusterRole), allRules(role)...)) {
			if triple.resource == "*" || triple.verb == "*" {
				t.Errorf("the Cilium Job is granted %s; it must hold no wildcard", triple)
			}
		}
	})

	// The Job runs in kube-system (hostNetwork is exempt from Pod Security admission only there); the release record's
	// Role stays in the release namespace.
	t.Run("lives in kube-system", func(t *testing.T) {
		docs := renderChart(t, "ichor")

		sa := find(docs, kindServiceAccount, "ichor-cni")
		if sa == nil || namespaceOf(sa) != kubeSystemNs {
			t.Errorf("ServiceAccount ichor-cni = %v, want it in kube-system", sa)
		}

		jobRole := find(docs, kindRole, "ichor-cni-job-role")
		if jobRole == nil || namespaceOf(jobRole) != kubeSystemNs {
			t.Fatalf("Role ichor-cni-job-role = %v, want it in kube-system", jobRole)
		}

		checkCNIRoleMatchesConfig(t, docs, kindRole, "cni-job-role", "controller_role.yaml")

		installerDocs := renderChartWithValues(t, "ichor", "installer.enabled=true")

		// Installer rights are cluster-scoped only: an upgrade runs under the previous
		// release's role, which cannot create a Role in kube-system, so the installer holds no
		// namespaced Role there and its ClusterRole carries what it needs to apply the Job's objects.
		for _, d := range installerDocs {
			if (kindOf(d) == kindRole || kindOf(d) == kindRoleBinding) && namespaceOf(d) == kubeSystemNs &&
				strings.HasPrefix(nameOf(d), "ichor-installer") {
				t.Errorf("%s %s is in kube-system; installer rights are cluster-scoped only", kindOf(d), nameOf(d))
			}
		}

		secrets := find(docs, kindRole, "ichor-cni-role")
		if secrets == nil || namespaceOf(secrets) == kubeSystemNs {
			t.Errorf("Role ichor-cni-role (release records) = %v, want it in the release namespace", secrets)
		}

		for _, b := range findAll(docs, "ClusterRoleBinding") {
			if nameOf(b) != "ichor-cni-rolebinding" {
				continue
			}

			subjects, _ := b["subjects"].([]any)
			s, _ := subjects[0].(map[string]any)

			if s["namespace"] != kubeSystemNs {
				t.Errorf("ClusterRoleBinding ichor-cni-rolebinding subject namespace = %v, want kube-system", s["namespace"])
			}
		}
	})

	t.Run("can be turned off", func(t *testing.T) {
		docs := renderChartWithValues(t, "ichor", "cniHelm.enabled=false")
		if find(docs, kindServiceAccount, "ichor-cni") != nil || find(docs, kindClusterRole, "ichor-cni-role") != nil {
			t.Error("cniHelm.enabled=false still renders the Cilium Job's identity")
		}
	})
}
