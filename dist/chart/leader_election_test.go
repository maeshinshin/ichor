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
	"slices"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// TestLeaderElectionRoleIsLeasesOnly guards ADR 0108: the manager locks with a Lease, so the
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

// TestWebhooksFailFastAndCoverFinalizersAndDeletes guards ADR 0108: every webhook (chart and
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

// TestKubeProxyPolicyGuardsAbsentFields guards ADR 0108: the kube-proxy switch policy reads
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

func namespaceOf(doc map[string]any) string {
	md, _ := doc["metadata"].(map[string]any)
	n, _ := md["namespace"].(string)

	return n
}

// escalationNames returns, per resource ("clusterroles", "roles"), the resourceNames of every rule
// of the role docs that grants bind or escalate on it, and reports a rule that grants either without
// naming roles.
func escalationNames(t *testing.T, label string, roles ...map[string]any) map[string]map[string]bool {
	t.Helper()

	out := map[string]map[string]bool{}

	for _, role := range roles {
		if role == nil {
			t.Fatalf("%s: role missing", label)
		}

		for _, r := range allRules(role) {
			rm, _ := r.(map[string]any)
			verbs := stringsOf(rm["verbs"])

			if !slices.Contains(verbs, "bind") && !slices.Contains(verbs, "escalate") {
				continue
			}

			names := stringsOf(rm["resourceNames"])
			for _, res := range stringsOf(rm["resources"]) {
				if len(names) == 0 {
					t.Errorf("%s: %s %s grants %v on %s without resourceNames (ADR 0108)", label, kindOf(role), nameOf(role), verbs, res)
				}

				if out[res] == nil {
					out[res] = map[string]bool{}
				}

				for _, n := range names {
					out[res][n] = true
				}
			}
		}
	}

	return out
}

func setOf(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}

	return m
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}

	for k := range a {
		if !b[k] {
			return false
		}
	}

	return true
}

// TestInstallerEscalationNamesEveryRoleOfTheInstallManifest guards ADR 0108: the installer's bind
// and escalate rights are limited to the ClusterRoles and Roles its own install manifest contains
// (so it cannot bind cluster-admin to anything), in config/rbac and in the chart, and the lists do
// not drift from dist/install.yaml. It needs `make build-installer`.
func TestInstallerEscalationNamesEveryRoleOfTheInstallManifest(t *testing.T) {
	var clusterRoles, allRoles, nsRoles []string

	for _, d := range loadYAMLDocs(t, "../install.yaml") {
		switch kindOf(d) {
		case kindClusterRole:
			clusterRoles = append(clusterRoles, nameOf(d))
		case kindRole:
			allRoles = append(allRoles, nameOf(d))
			if namespaceOf(d) == "ichor-system" {
				nsRoles = append(nsRoles, nameOf(d))
			}
		}
	}

	cfgDocs := loadYAMLDocs(t, installerConfigRolePath)
	chartDocs := renderChartWithValues(t, "ichor", "installer.enabled=true")

	for label, docs := range map[string][]map[string]any{"config/rbac": cfgDocs, "chart": chartDocs} {
		cr, rl := "installer-role", "installer-role"
		if label == "chart" {
			cr, rl = "ichor-installer-role", "ichor-installer-role"
		}

		clusterLevel := escalationNames(t, label, find(docs, kindClusterRole, cr))
		if !sameSet(clusterLevel["clusterroles"], setOf(clusterRoles)) {
			t.Errorf("%s: installer ClusterRole bind/escalate on clusterroles names %v, install.yaml has %v", label, clusterLevel["clusterroles"], clusterRoles)
		}

		if !sameSet(clusterLevel["roles"], setOf(allRoles)) {
			t.Errorf("%s: installer ClusterRole bind/escalate on roles names %v, install.yaml has %v", label, clusterLevel["roles"], allRoles)
		}

		nsLevel := escalationNames(t, label, find(docs, kindRole, rl))
		if !sameSet(nsLevel["roles"], setOf(nsRoles)) {
			t.Errorf("%s: installer Role bind/escalate on roles names %v, install.yaml has %v in ichor-system", label, nsLevel["roles"], nsRoles)
		}
	}
}

// TestCNIEscalationNamesOnlyCiliumsRoles guards ADR 0108: the Cilium Job may bind and escalate only
// the roles the pinned Cilium chart creates, in config/rbac and in the chart. Every role the chart
// renders under Ichor's values must be named, so a chart bump that adds one fails here, not in a cluster.
func TestCNIEscalationNamesOnlyCiliumsRoles(t *testing.T) {
	rendered := map[string]map[string]bool{"clusterroles": {}, "roles": {}}

	for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
		for _, tunnel := range []string{"", "geneve", "vxlan"} {
			p := cilium.New()
			if tunnel != "" {
				var err error
				if p, err = p.WithTunnel(tunnel); err != nil {
					t.Fatal(err)
				}
			}

			manifest, err := p.BootstrapManifest(mode)
			if err != nil {
				t.Fatalf("rendering cilium (%s, %q): %v", mode, tunnel, err)
			}

			for _, d := range decodeYAMLDocs(t, manifest, "cilium bootstrap") {
				switch kindOf(d) {
				case kindClusterRole:
					rendered["clusterroles"][nameOf(d)] = true
				case kindRole:
					rendered["roles"][nameOf(d)] = true
				}
			}
		}
	}

	cfgDocs := loadYAMLDocs(t, "../../config/rbac/ichor-cni/role.yaml")
	chartDocs := renderChartWithValues(t, "ichor", "cniHelm.enabled=true")

	for label, role := range map[string]map[string]any{
		"config/rbac": find(cfgDocs, kindClusterRole, "cni-role"),
		"chart":       find(chartDocs, kindClusterRole, "ichor-cni-role"),
	} {
		got := escalationNames(t, label, role)
		for res, names := range rendered {
			for n := range names {
				if !got[res][n] {
					t.Errorf("%s: the Cilium chart renders %s %q, which the CNI role cannot bind or escalate", label, res, n)
				}
			}
		}
	}
}
