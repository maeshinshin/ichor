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

package cli

import (
	"fmt"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const renderedHostname = "HostnameConfig.hostname"

// Adopt --add-machine's refusal of a machine whose only differences are documents Ichor rendered from
// its deleted TalosMachine offers the wipe, and reclaim node accepts that machine (pkg/reclaim
// TestAssess_OrphanOfManagedClusterWarnsAndDoesNotLoop): the two refusals do not point at each other.
func TestAdoptRefusalOffersTheWipe(t *testing.T) {
	st := &adoptState{
		adding:  true,
		cluster: &ichorv1alpha1.TalosCluster{},
		nodes: []*adoptNode{{
			added: true,
			M:     &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Endpoint: "10.0.0.21:50000"}},
		}},
	}
	st.cluster.Name = ownClusterName
	st.nodes[0].M.Name = "w1"

	for name, paths := range map[string][]string{
		"only rendered documents": {renderedHostname, "LinkAliasConfig.selector", "DHCPv4Config.name"},
		"other fields as well":    {renderedHostname, "KubeNetworkConfig.podSubnets"},
	} {
		var lines []string

		st.hintStartOver(func(_ adoptLevel, format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		}, []*adoptPlan{{Machine: "w1", UnrepresentablePaths: paths}})

		got := strings.Join(lines, "\n")
		if !strings.Contains(got, "ichor reclaim node 10.0.0.21 --cluster "+ownClusterName) || !strings.Contains(got, "UI Reset and add") {
			t.Errorf("%s: the refusal does not offer the wipe:\n%s", name, got)
		}

		if rendered := strings.Contains(got, "renders from a node's TalosMachine spec"); rendered != (name == "only rendered documents") {
			t.Errorf("%s: explanation of the rendered documents = %v:\n%s", name, rendered, got)
		}
	}

	// A plain adoption (not --add-machine) says nothing about wiping.
	st.adding = false

	st.hintStartOver(func(adoptLevel, string, ...any) { t.Error("hint outside --add-machine") },
		[]*adoptPlan{{Machine: "w1", UnrepresentablePaths: []string{renderedHostname}}})
}
