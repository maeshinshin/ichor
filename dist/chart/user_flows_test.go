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
	"slices"
	"strings"
	"testing"
)

// TestBuiltinRolesRunTheDocumentedFlows pins, in config/rbac and in the chart, that each built-in role holds
// every verb the flows it is meant to run make as the user (testdata/user_flows.yaml). A role that has the
// download verb but not the create on taloscredentialrequests, or an admin who cannot patch a
// TalosEtcdBackup, fails here.
func TestBuiltinRolesRunTheDocumentedFlows(t *testing.T) {
	chartDocs := renderChartWithValues(t, "ichor", usersOn, usersURL)

	flows, _ := loadYAMLDocs(t, "testdata/user_flows.yaml")[0]["flows"].([]any)
	if len(flows) == 0 {
		t.Fatal("testdata/user_flows.yaml lists no flows")
	}

	for _, raw := range flows {
		f, _ := raw.(map[string]any)
		name, _ := f["name"].(string)
		wantRole, _ := f["role"].(string)
		lowest := slices.Index(builtinUserRoles, wantRole)

		if lowest < 0 {
			t.Fatalf("flow %q names the unknown role %v", name, f["role"])
		}

		for _, role := range builtinUserRoles[lowest:] {
			cfgDocs := loadYAMLDocs(t, "../../config/rbac/ichor_"+role+"_role.yaml")
			sources := map[string]map[rbacTriple]bool{
				labelChart:  rbacTriples(allRules(find(chartDocs, kindClusterRole, "ichor-"+role))),
				labelConfig: rbacTriples(allRules(find(cfgDocs, kindClusterRole, role))),
			}

			for label, got := range sources {
				for _, need := range stringsOf(f["needs"]) {
					resource, verb, _ := strings.Cut(need, " ")
					if !grantedOn(got, [2]string{ichorGroup, resource}, verb, "") {
						t.Errorf("%s: ClusterRole ichor-%s cannot %s %s, which the flow %q needs",
							label, role, verb, resource, name)
					}
				}
			}
		}
	}
}
