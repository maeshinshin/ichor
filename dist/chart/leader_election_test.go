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

// TestLeaderElectionRoleIsLeasesOnly guards: the manager locks with a Lease, so the
// leader-election Role holds no ConfigMap rights, in the chart and in config/rbac alike.
func TestLeaderElectionRoleIsLeasesOnly(t *testing.T) {
	chartRole := findNameSuffix(renderChart(t, "ichor"), kindRole, "leader-election-role")
	cfgRole := find(loadYAMLDocs(t, "../../config/rbac/leader_election_role.yaml"), kindRole, "leader-election-role")

	if chartRole == nil || cfgRole == nil {
		t.Fatalf("leader election Role missing: chart=%v config=%v", chartRole != nil, cfgRole != nil)
	}

	for label, role := range map[string]map[string]any{"chart": chartRole, "config/rbac": cfgRole} {
		got := rbacTriples(allRules(role))
		if !got[rbacTriple{"coordination.k8s.io", "leases", "create", ""}] {
			t.Errorf("%s: the leader-election Role cannot create leases", label)
		}

		for tr := range got {
			if tr.resource == "configmaps" {
				t.Errorf("%s: the leader-election Role grants %s, which a Lease lock does not use", label, tr)
			}
		}
	}
}
