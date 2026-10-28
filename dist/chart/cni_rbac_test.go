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

import "testing"

// cniConfigRolePath is config/rbac/ichor-cni/role.yaml (hand-written, ADR 0099): the Cilium
// Helm Job's ClusterRole, whose chart copy templates/rbac/ichor-cni.yaml is compared as a rule set.
const cniConfigRolePath = "../../config/rbac/ichor-cni/role.yaml"

const adr0099Doc = "docs/adr/0099-argo-cd-and-kokumi-become-the-standard-stack.md"

// TestCNIHelmRBAC pins the Cilium Helm Job's identity (ADR 0099): the ServiceAccount
// ichor-cni the controller names in the Job exists, its ClusterRole equals config/rbac/ichor-cni,
// it can be turned off, and it holds no wildcard.
func TestCNIHelmRBAC(t *testing.T) {
	t.Run("matches config", func(t *testing.T) {
		docs := renderChart(t, "ichor")
		configDocs := loadYAMLDocs(t, cniConfigRolePath)

		if find(docs, kindServiceAccount, "ichor-cni") == nil {
			t.Fatalf("the chart renders no ServiceAccount ichor-cni, the name the controller puts in the Job; see %s",
				adr0099Doc)
		}

		clusterRole := find(docs, kindClusterRole, "ichor-cni-role")

		checkRBACRulesMatch(t, "ichor", "ClusterRole cni-role",
			find(configDocs, kindClusterRole, "cni-role"), clusterRole,
			cniConfigRolePath, "dist/chart/templates/rbac/ichor-cni.yaml")

		role := find(docs, kindRole, "ichor-cni-role")
		checkRBACRulesMatch(t, "ichor", "Role cni-role",
			find(configDocs, kindRole, "cni-role"), role,
			cniConfigRolePath, "dist/chart/templates/rbac/ichor-cni.yaml")

		for triple := range rbacTriples(append(allRules(clusterRole), allRules(role)...)) {
			if triple.resource == "*" || triple.verb == "*" {
				t.Errorf("the Cilium Job is granted %s; it must hold no wildcard; see %s", triple, adr0099Doc)
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
