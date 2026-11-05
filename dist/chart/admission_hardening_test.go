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
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

const (
	labelConfig  = "config/rbac"
	labelChart   = "chart"
	verbOverride = "override"
)

// TestLeaderElectionRoleIsLeasesOnly guards: the manager locks with a Lease, so the
// leader-election Role holds no ConfigMap rights, in the chart and in config/rbac alike.
func TestLeaderElectionRoleIsLeasesOnly(t *testing.T) {
	chartRole := findNameSuffix(renderChart(t, "ichor"), kindRole, "leader-election-role")
	cfgRole := find(loadYAMLDocs(t, "../../config/rbac/leader_election_role.yaml"), kindRole, "leader-election-role")

	if chartRole == nil || cfgRole == nil {
		t.Fatalf("leader election Role missing: chart=%v config=%v", chartRole != nil, cfgRole != nil)
	}

	for label, role := range map[string]map[string]any{labelChart: chartRole, labelConfig: cfgRole} {
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
// override finalizer check also sees TalosOperation updates; the IchorInstallation check sees
// deletes.
func TestWebhooksFailFastAndCoverFinalizersAndDeletes(t *testing.T) {
	docs := renderChart(t, "ichor")

	sets := map[string][]map[string]any{
		labelChart: docs, "config/webhook": loadYAMLDocs(t, "../../config/webhook/manifests.yaml"),
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
			ops := make([]string, 0, len(rules))

			for _, r := range rules {
				rm, _ := r.(map[string]any)
				ops = append(ops, stringsOf(rm["operations"])...)
			}

			return strings.Join(ops, ",")
		}

		if !strings.Contains(opsOf("voverrideop-v1alpha1.kb.io"), "UPDATE") {
			t.Errorf("%s: voverrideop does not see UPDATE, so a TalosOperation finalizer can be stripped unchecked", label)
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

		guards := []string{"has(object.spec.cni)", "has(oldObject.spec.cni)", "has(oldObject.spec.cni.kubeProxy)"}
		for _, want := range guards {
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
					t.Errorf("%s: %s %s grants %v on %s without resourceNames",
						label, kindOf(role), nameOf(role), verbs, res)
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

// TestInstallerEscalationNamesEveryRoleOfTheInstallManifest guards: the installer's bind
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

	for label, docs := range map[string][]map[string]any{labelConfig: cfgDocs, labelChart: chartDocs} {
		cr, rl := "installer-role", "installer-role"
		if label == labelChart {
			cr, rl = "ichor-installer-role", "ichor-installer-role"
		}

		clusterLevel := escalationNames(t, label, find(docs, kindClusterRole, cr))
		if !maps.Equal(clusterLevel[resClusterRoles], setOf(clusterRoles)) {
			t.Errorf("%s: installer ClusterRole bind/escalate on clusterroles names %v, install.yaml has %v",
				label, clusterLevel[resClusterRoles], clusterRoles)
		}

		if !maps.Equal(clusterLevel["roles"], setOf(allRoles)) {
			t.Errorf("%s: installer ClusterRole bind/escalate on roles names %v, install.yaml has %v",
				label, clusterLevel["roles"], allRoles)
		}

		nsLevel := escalationNames(t, label, find(docs, kindRole, rl))
		if !maps.Equal(nsLevel["roles"], setOf(nsRoles)) {
			t.Errorf("%s: installer Role bind/escalate on roles names %v, install.yaml has %v in ichor-system",
				label, nsLevel["roles"], nsRoles)
		}
	}
}

func userValues(js string) map[string]any {
	var v map[string]any
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		panic(err)
	}

	return v
}

// gatewayAPIValues is the user layer that makes the Cilium chart render its Gateway API objects. The
// GatewayClass is created "auto" only when the cluster has the CRD, which a plain render does not, so it is
// forced here.
var gatewayAPIValues = userValues(`{"gatewayAPI":{"enabled":true,"gatewayClass":{"create":"true"}}}`)

// optionalCiliumFeatures are the user layers that turn on the optional Cilium features whose objects the CNI
// role has to cover. Hubble UI needs the relay (the chart refuses it otherwise), and
// the Cluster Mesh API server needs the cronJob certificate method like Hubble's: the chart's default,
// "helm", renders Secrets in kube-system, which the Job's ServiceAccount cannot manage.
var optionalCiliumFeatures = []struct {
	name   string
	values map[string]any
}{
	{"gatewayAPI", gatewayAPIValues},
	{"ingressController", userValues(`{"ingressController":{"enabled":true}}`)},
	{"hubbleUI", userValues(`{"hubble":{"relay":{"enabled":true},"ui":{"enabled":true}}}`)},
	{"clustermesh", userValues(`{"clustermesh":{"useAPIServer":true,"apiserver":{"tls":{"auto":{"method":"cronJob"}}}}}`)},
	{"bgpControlPlane", userValues(`{"bgpControlPlane":{"enabled":true}}`)},
	{"envoyConfig", userValues(`{"envoyConfig":{"enabled":true}}`)},
}

// cniClusterRoles is the Cilium Job's ClusterRole as config/rbac and the chart carry it.
func cniClusterRoles(t *testing.T) map[string]map[string]any {
	t.Helper()

	return map[string]map[string]any{
		labelConfig: find(loadYAMLDocs(t, "../../config/rbac/ichor-cni/role.yaml"), kindClusterRole, "cni-role"),
		labelChart:  find(renderChartWithValues(t, "ichor", "cniHelm.enabled=true"), kindClusterRole, "ichor-cni-role"),
	}
}

// TestCNIEscalationNamesOnlyCiliumsRoles guards: the Cilium Job may bind and escalate only
// the roles the pinned Cilium chart creates, in config/rbac and in the chart. Every role the chart
// renders under Ichor's values must be named, so a chart bump that adds one fails here, not in a cluster.
func TestCNIEscalationNamesOnlyCiliumsRoles(t *testing.T) {
	rendered := map[string]map[string]bool{resClusterRoles: {}, "roles": {}}

	for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
		for _, tunnel := range []string{"", "geneve", "vxlan"} {
			users := make([]map[string]any, 1, 1+len(optionalCiliumFeatures)) // nil first: no user values
			for _, f := range optionalCiliumFeatures {
				users = append(users, f.values)
			}

			for _, user := range users {
				p := cilium.New().WithValues(user)
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
						rendered[resClusterRoles][nameOf(d)] = true
					case kindRole:
						rendered["roles"][nameOf(d)] = true
					}
				}
			}
		}
	}

	for label, role := range cniClusterRoles(t) {
		got := escalationNames(t, label, role)
		for res, names := range rendered {
			for n := range names {
				if !got[res][n] {
					t.Errorf("%s: the Cilium chart renders %s %q, which the CNI role cannot bind or escalate", label, res, n)
				}
			}
		}

		// The other way: a name the chart no longer renders is a grant nothing needs.
		for res, names := range got {
			for n := range names {
				if !rendered[res][n] {
					t.Errorf("%s: the CNI role binds or escalates %s %q, which the Cilium chart does not render", label, res, n)
				}
			}
		}
	}
}

// TestCNIRoleCoversTheOptionalFeatureObjects: every object the Cilium chart renders with an optional feature
// on must be one the CNI role lets Helm read, create, update and delete, or the upgrade fails on the first
// get (the Gateway API case found on hardware). Kinds are mapped to resources here, so a
// chart bump that renders a kind nobody mapped fails too. Role and ClusterRole names are
// TestCNIEscalationNamesOnlyCiliumsRoles's business.
func TestCNIRoleCoversTheOptionalFeatureObjects(t *testing.T) {
	resource := map[string]string{}

	for pair := range strings.FieldsSeq("ConfigMap=configmaps Service=services ServiceAccount=serviceaccounts " +
		"Namespace=namespaces Deployment=deployments DaemonSet=daemonsets Job=jobs CronJob=cronjobs " +
		"PodDisruptionBudget=poddisruptionbudgets Role=roles RoleBinding=rolebindings ClusterRole=clusterroles " +
		"ClusterRoleBinding=clusterrolebindings IngressClass=ingressclasses EndpointSlice=endpointslices " +
		"GatewayClass=gatewayclasses ValidatingAdmissionPolicy=validatingadmissionpolicies " +
		"ValidatingAdmissionPolicyBinding=validatingadmissionpolicybindings") {
		kind, res, _ := strings.Cut(pair, "=")
		resource[kind] = res
	}

	roles := cniClusterRoles(t)

	for _, f := range optionalCiliumFeatures {
		t.Run(f.name, func(t *testing.T) {
			manifest, err := cilium.New().WithValues(f.values).BootstrapManifest(cni.KubeProxyEnabled)
			if err != nil {
				t.Fatal(err)
			}

			kinds := map[[2]string]bool{} // apiGroup, kind

			for _, d := range decodeYAMLDocs(t, manifest, "cilium bootstrap") {
				group, _, hasGroup := strings.Cut(d["apiVersion"].(string), "/")
				if !hasGroup {
					group = "" // the core group: apiVersion is just "v1"
				}

				kinds[[2]string{group, kindOf(d)}] = true
			}

			// The test must not pass for nothing: the Gateway API values really render a GatewayClass.
			if f.name == "gatewayAPI" && !kinds[[2]string{"gateway.networking.k8s.io", "GatewayClass"}] {
				t.Fatal("the Cilium chart renders no GatewayClass with gatewayAPI on")
			}

			for label, role := range roles {
				for k := range kinds {
					res, ok := resource[k[1]]
					if !ok {
						t.Errorf("%s: the chart renders a %s (group %q) this test does not map to a resource, "+
							"so the CNI role may not cover it", label, k[1], k[0])

						continue
					}

					held := map[string]bool{}

					for _, r := range allRules(role) {
						rm, _ := r.(map[string]any)
						if !slices.Contains(stringsOf(rm["apiGroups"]), k[0]) || !slices.Contains(stringsOf(rm["resources"]), res) {
							continue
						}

						for _, v := range stringsOf(rm["verbs"]) {
							held[v] = true
						}
					}

					for v := range strings.FieldsSeq("get list create update patch delete") {
						if !held[v] {
							t.Errorf("%s: the CNI role cannot %s %s.%s, which the Cilium chart renders with %s on",
								label, v, res, k[0], f.name)
						}
					}
				}
			}
		})
	}
}

// cniNamedObjects are the cluster-scoped and infrastructure kinds the CNI role may change only by name: a Cilium Job
// that can update or delete any ValidatingAdmissionPolicy could remove the
// ichor-controller-jobs policies that confine it. Create cannot be limited by name in RBAC and stays.
var cniNamedObjects = map[string][2]string{ // kind: apiGroup, resource
	"ValidatingAdmissionPolicy":        {"admissionregistration.k8s.io", "validatingadmissionpolicies"},
	"ValidatingAdmissionPolicyBinding": {"admissionregistration.k8s.io", "validatingadmissionpolicybindings"},
	"GatewayClass":                     {"gateway.networking.k8s.io", "gatewayclasses"},
	"IngressClass":                     {"networking.k8s.io", "ingressclasses"},
	"EndpointSlice":                    {"discovery.k8s.io", "endpointslices"},
}

// TestCNIRoleChangesOnlyTheObjectsCiliumRenders: get, update, patch and delete on the kinds above are granted
// by resourceNames, and the names are exactly those the pinned Cilium chart renders with every optional feature
// on, so a chart bump that renames one fails here and the Job cannot touch the Ichor Job policies, in config/rbac
// and in the chart alike.
func TestCNIRoleChangesOnlyTheObjectsCiliumRenders(t *testing.T) {
	rendered := map[string]map[string]bool{} // resource: names

	for _, f := range optionalCiliumFeatures {
		manifest, err := cilium.New().WithValues(f.values).BootstrapManifest(cni.KubeProxyEnabled)
		if err != nil {
			t.Fatal(err)
		}

		for _, d := range decodeYAMLDocs(t, manifest, "cilium bootstrap") {
			if gr, ok := cniNamedObjects[kindOf(d)]; ok {
				if rendered[gr[1]] == nil {
					rendered[gr[1]] = map[string]bool{}
				}

				rendered[gr[1]][nameOf(d)] = true
			}
		}
	}

	for kind, gr := range cniNamedObjects {
		if len(rendered[gr[1]]) == 0 {
			t.Fatalf("the Cilium chart renders no %s under any optional feature; the check would pass for nothing", kind)
		}
	}

	cfgDocs := loadYAMLDocs(t, "../../config/rbac/ichor-cni/role.yaml")
	chartDocs := renderChartWithValues(t, "ichor", "cniHelm.enabled=true")

	for label, role := range map[string]map[string]any{
		labelConfig: find(cfgDocs, kindClusterRole, "cni-role"),
		labelChart:  find(chartDocs, kindClusterRole, "ichor-cni-role"),
	} {
		for _, gr := range cniNamedObjects {
			for _, r := range allRules(role) {
				rm, _ := r.(map[string]any)
				if !slices.Contains(stringsOf(rm["apiGroups"]), gr[0]) || !slices.Contains(stringsOf(rm["resources"]), gr[1]) {
					continue
				}

				names := stringsOf(rm["resourceNames"])
				for _, v := range stringsOf(rm["verbs"]) {
					switch v {
					case verbCreateController, verbList, verbWatch:
						if len(names) != 0 {
							t.Errorf("%s: %s on %s carries resourceNames, which RBAC cannot apply to it", label, v, gr[1])
						}
					default:
						if len(names) == 0 {
							t.Errorf("%s: the CNI role grants %s on every %s; limit it to the names Cilium renders", label, v, gr[1])
						}

					}
				}

				for _, n := range names {
					if !rendered[gr[1]][n] {
						t.Errorf("%s: resourceNames of %s lists %q, which the Cilium chart does not render", label, gr[1], n)
					}
				}
			}

			for name := range rendered[gr[1]] {
				for v := range strings.FieldsSeq("get update patch delete") {
					if !cniHolds(allRules(role), gr[0], gr[1], v, name) {
						t.Errorf("%s: the CNI role cannot %s %s %q, which the Cilium chart renders", label, v, gr[1], name)
					}
				}
			}

			for _, v := range []string{verbCreateController, verbList} {
				if !cniHolds(allRules(role), gr[0], gr[1], v, "") {
					t.Errorf("%s: the CNI role cannot %s %s without a name, which Helm needs", label, v, gr[1])
				}
			}

			// The Job's own confinement is never among the names it may change.
			for protected := range strings.FieldsSeq("ichor-controller-jobs ichor-controller-job-image " +
				"ichor-controller-jobs-binding ichor-controller-job-image-binding") {
				for v := range strings.FieldsSeq("get update patch delete") {
					if cniHolds(allRules(role), gr[0], gr[1], v, protected) {
						t.Errorf("%s: the CNI role can %s %s %q, one of the policies that confine it", label, v, gr[1], protected)
					}
				}
			}
		}
	}
}

// TestCNIRoleHoldsEveryRuleTheCiliumChartGrants guards (the 1.19.10 takeover
// failure): Helm refuses to create a Role that grants a rule its caller does not hold. The Cilium
// chart's hubble-generate-certs Role, which the Job creates on a takeover, must have every one of its
// rules held by the Cilium Job's ServiceAccount through its bindings in kube-system, in config/rbac/ichor-cni
// and in the chart. A chart bump that changes that Role fails here, not on a node. Other Cilium Roles and
// ClusterRoles are not enforced here: they exist on a takeover and Helm does not create them.
func TestCNIRoleHoldsEveryRuleTheCiliumChartGrants(t *testing.T) {
	var roles []map[string]any

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
				if kindOf(d) == kindRole && namespaceOf(d) == kubeSystemNs && nameOf(d) == "hubble-generate-certs" {
					roles = append(roles, d)
				}
			}
		}
	}

	if len(roles) == 0 {
		t.Fatal("the Cilium chart renders no hubble-generate-certs Role in kube-system; the check would pass for nothing")
	}

	cfgDocs := loadYAMLDocs(t, "../../config/rbac/ichor-cni/role.yaml")
	for _, f := range []string{"role_binding.yaml", "records_role.yaml", "hubble_role.yaml"} {
		cfgDocs = append(cfgDocs, loadYAMLDocs(t, "../../config/rbac/ichor-cni/"+f)...)
	}

	for label, docs := range map[string][]map[string]any{
		labelConfig: cfgDocs,
		labelChart:  renderChartWithValues(t, "ichor", "cniHelm.enabled=true"),
	} {
		held := cniGrantedRules(docs, kubeSystemNs)
		for _, r := range roles {
			checkCNIHolds(t, label, r, held)
		}
	}
}

// cniGrantedRules returns the rules of the ClusterRoles and Roles that bind the Cilium Job's
// ServiceAccount in namespace ns (ns "" counts cluster-wide bindings only).
func cniGrantedRules(docs []map[string]any, ns string) []any {
	roles := map[string]map[string]any{}

	for _, d := range docs {
		if k := kindOf(d); k == kindRole || k == kindClusterRole {
			roles[k+"/"+namespaceOf(d)+"/"+nameOf(d)] = d
		}
	}

	var rules []any

	for _, b := range docs {
		k := kindOf(b)
		if k != kindClusterRoleBinding && (k != kindRoleBinding || ns == "" || namespaceOf(b) != ns) {
			continue
		}

		if !bindsCNIJob(b) {
			continue
		}

		ref, _ := b["roleRef"].(map[string]any)
		rk, _ := ref["kind"].(string)
		rn, _ := ref["name"].(string)

		refNS := ""
		if rk == kindRole {
			refNS = namespaceOf(b)
		}

		rules = append(rules, allRules(roles[rk+"/"+refNS+"/"+rn])...)
	}

	return rules
}

// bindsCNIJob reports whether a binding's subjects include the Cilium Job's ServiceAccount in kube-system.
// Config names it "cni" (kustomize adds the prefix), the chart and some config files "ichor-cni".
func bindsCNIJob(b map[string]any) bool {
	subjects, _ := b["subjects"].([]any)

	for _, s := range subjects {
		sm, _ := s.(map[string]any)
		name, _ := sm["name"].(string)
		ns, _ := sm["namespace"].(string)

		if sm["kind"] == "ServiceAccount" && ns == kubeSystemNs && (name == "ichor-cni" || name == "cni") {
			return true
		}
	}

	return false
}

// checkCNIHolds reports each (group, resource, verb, resourceName) that role grants and held does not.
func checkCNIHolds(t *testing.T, label string, role map[string]any, held []any) {
	t.Helper()

	for _, r := range allRules(role) {
		rm, _ := r.(map[string]any)

		names := stringsOf(rm["resourceNames"])
		if len(names) == 0 {
			names = []string{""}
		}

		for _, g := range stringsOf(rm["apiGroups"]) {
			for _, res := range stringsOf(rm["resources"]) {
				for _, v := range stringsOf(rm["verbs"]) {
					for _, n := range names {
						if !cniHolds(held, g, res, v, n) {
							t.Errorf("%s: %s %s grants %s/%s:%s (name %q) that the Cilium Job does not hold; Helm refuses it",
								label, kindOf(role), nameOf(role), g, res, v, n)
						}
					}
				}
			}
		}
	}
}

// cniHolds reports whether one of the held rules grants (group, resource, verb) on name, an empty
// name meaning an unnamed grant that only a rule without resourceNames gives.
func cniHolds(held []any, group, res, verb, name string) bool {
	match := func(list []string, v string) bool { return slices.Contains(list, "*") || slices.Contains(list, v) }

	for _, r := range held {
		rm, _ := r.(map[string]any)
		if !match(stringsOf(rm["apiGroups"]), group) || !match(stringsOf(rm["resources"]), res) ||
			!match(stringsOf(rm["verbs"]), verb) {
			continue
		}

		names := stringsOf(rm["resourceNames"])
		if len(names) == 0 || (name != "" && slices.Contains(names, name)) {
			return true
		}
	}

	return false
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
			t.Errorf("policy %s: chart validations differ from config/admission/controller-jobs-policy.yaml\n"+
				"chart:  %q\nconfig: %q", name, kept, wantExprs)
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

	for label, docs := range map[string][]map[string]any{labelChart: chartDocs, labelConfig: cfgDocs} {
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
