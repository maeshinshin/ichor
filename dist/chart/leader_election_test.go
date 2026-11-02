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
	"fmt"
	"os"
	"strings"
	"testing"
)

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

// TestWebhooksFailFastAndCoverFinalizersAndDeletes guards: every webhook (chart and
// config/webhook) answers within 5 seconds, so an unavailable manager refuses writes quickly; the
// break-glass finalizer check also sees TalosOperation updates; the IchorInstallation check sees deletes.
func TestWebhooksFailFastAndCoverFinalizersAndDeletes(t *testing.T) {
	docs := renderChart(t, "ichor")

	sets := map[string][]map[string]any{"chart": docs}
	for _, f := range []string{"../../config/webhook/manifests.yaml"} {
		sets["config/webhook"] = loadYAMLDocs(t, f)
	}

	for label, set := range sets {
		hooks := map[string]map[string]any{}

		for _, kind := range []string{"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration"} {
			for _, d := range findAll(set, kind) {
				ws, _ := d["webhooks"].([]any)
				for _, w := range ws {
					wm, _ := w.(map[string]any)
					name, _ := wm["name"].(string)
					hooks[name] = wm

					if got, ok := wm["timeoutSeconds"].(int64); !ok && fmt.Sprint(wm["timeoutSeconds"]) != "5" {
						t.Errorf("%s: webhook %s has timeoutSeconds %v, want 5", label, name, got)
					}
				}
			}
		}

		opsOf := func(name string) string {
			rules, _ := hooks[name]["rules"].([]any)
			var ops []string

			for _, r := range rules {
				rm, _ := r.(map[string]any)
				ops = append(ops, stringsOf(rm["operations"])...)
			}

			return strings.Join(ops, ",")
		}

		if !strings.Contains(opsOf("vbreakglassop-v1alpha1.kb.io"), "UPDATE") {
			t.Errorf("%s: vbreakglassop does not see UPDATE, so a TalosOperation finalizer can be stripped unchecked", label)
		}

		if !strings.Contains(opsOf("vichorinstallation-v1alpha1.kb.io"), "DELETE") {
			t.Errorf("%s: vichorinstallation does not see DELETE", label)
		}
	}
}

// TestKubeProxyPolicyGuardsAbsentFields guards: the kube-proxy switch policy reads
// spec.cni.kubeProxy behind has(), so an object without it cannot make the CEL fail and, with
// failurePolicy Fail, refuse every update of the cluster. Chart and config/admission agree.
func TestKubeProxyPolicyGuardsAbsentFields(t *testing.T) {
	for _, path := range []string{
		"../../config/admission/kubeproxy-switch-policy.yaml",
		"templates/admission/kubeproxy-switch-policy.yaml",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		for _, want := range []string{"has(object.spec.cni)", "has(oldObject.spec.cni)", "has(oldObject.spec.cni.kubeProxy)"} {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s lacks the guard %q", path, want)
			}
		}

		if strings.Contains(string(b), "== oldObject.spec.cni.kubeProxy") {
			t.Errorf("%s still reads oldObject.spec.cni.kubeProxy unguarded", path)
		}
	}
}
