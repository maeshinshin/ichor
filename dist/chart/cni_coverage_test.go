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
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

const resRoles = "roles"

// ciliumRoleVariant is one Role or ClusterRole the embedded Cilium chart renders, with the feature
// (user values layer) that made it appear.
type ciliumRoleVariant struct {
	feature string
	role    map[string]any
}

// renderedCiliumRoles renders the chart without user values (every kube-proxy mode and tunnel: the
// base objects a bootstrap or a takeover leaves in the cluster) and once per optional feature.
func renderedCiliumRoles(t *testing.T) (base []ciliumRoleVariant, features []ciliumRoleVariant) {
	t.Helper()

	collect := func(feature string, p cilium.Preset, mode cni.KubeProxyMode, into *[]ciliumRoleVariant) {
		manifest, err := p.BootstrapManifest(mode)
		if err != nil {
			t.Fatalf("rendering cilium (%s, %s): %v", feature, mode, err)
		}

		for _, d := range decodeYAMLDocs(t, manifest, "cilium bootstrap") {
			if k := kindOf(d); k == kindRole || k == kindClusterRole {
				*into = append(*into, ciliumRoleVariant{feature, d})
			}
		}
	}

	for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
		for _, tunnel := range []string{"", "geneve", "vxlan"} {
			p := cilium.New()
			if tunnel != "" {
				var err error
				if p, err = p.WithTunnel(tunnel); err != nil {
					t.Fatal(err)
				}
			}

			collect("base", p, mode, &base)
		}
	}

	for _, f := range optionalCiliumFeatures {
		collect(f.name, cilium.New().WithValues(f.values), cni.KubeProxyEnabled, &features)
	}

	return base, features
}

// uncoveredByCNIJob returns every (group, resource, verb, name) a role grants that the Cilium Job's
// ServiceAccount does not hold itself, with Kubernetes' covers() semantics (apiserver RBAC escalation
// prevention): per group, resource, verb and resourceName, where a rule without resourceNames covers
// named ones but not the other way round. A ClusterRole is judged against the cluster-wide bindings, a
// Role against those plus the RoleBindings in its own namespace.
func uncoveredByCNIJob(docs []map[string]any, role map[string]any) []string {
	ns := ""
	if kindOf(role) == kindRole {
		ns = namespaceOf(role)
	}

	held := cniGrantedRules(docs, ns)

	var out []string

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
							out = append(out, fmt.Sprintf("%s/%s:%s name %q", g, res, v, n))
						}
					}
				}
			}
		}

		for _, u := range stringsOf(rm["nonResourceURLs"]) {
			for _, v := range stringsOf(rm["verbs"]) {
				if !cniHoldsURL(held, u, v) {
					out = append(out, fmt.Sprintf("nonResourceURL %s:%s", u, v))
				}
			}
		}
	}

	return out
}

// cniHoldsURL: a held non-resource rule covers url when it lists it, "*", or a prefix "p*" of it.
func cniHoldsURL(held []any, url, verb string) bool {
	for _, r := range held {
		rm, _ := r.(map[string]any)
		if !slices.Contains(stringsOf(rm["verbs"]), verb) && !slices.Contains(stringsOf(rm["verbs"]), "*") {
			continue
		}

		for _, u := range stringsOf(rm["nonResourceURLs"]) {
			if u == "*" || u == url || (strings.HasSuffix(u, "*") && strings.HasPrefix(url, strings.TrimSuffix(u, "*"))) {
				return true
			}
		}
	}

	return false
}

// TestCNIJobHoldsWhatTheCiliumRolesItCreatesGrant guards:
// the apiserver refuses to create or update a Role or ClusterRole whose rules the caller does not hold,
// unless the caller may `escalate` it, and that check sees no object name on a CREATE, so `escalate` by
// resourceNames never authorizes a creation (found on hardware: the Cilium Helm Job could not create
// cilium-gateway-secrets, hubble-ui, clustermesh-apiserver, ...). So every Role and ClusterRole the
// pinned chart renders must be one of two things, in config/rbac and in the chart alike:
//   - held: the Job's ServiceAccount covers every rule itself (an "escalation would succeed" check), or
//   - a base object (rendered with no optional feature, so it exists from the bootstrap or the takeover and
//     Helm only updates it) that the Job may bind and escalate by name.
//
// The names lists hold exactly those base objects, no more: a name the Job holds anyway is a grant nothing needs.
func TestCNIJobHoldsWhatTheCiliumRolesItCreatesGrant(t *testing.T) {
	base, features := renderedCiliumRoles(t)

	var cfgDocs []map[string]any

	files, err := filepath.Glob("../../config/rbac/ichor-cni/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no config/rbac/ichor-cni files: %v", err)
	}

	for _, f := range files {
		if !strings.HasSuffix(f, "kustomization.yaml") {
			cfgDocs = append(cfgDocs, loadYAMLDocs(t, f)...)
		}
	}

	chartDocs := renderChartWithValues(t, "ichor", "cniHelm.ciliumSecretsRoles=true")

	baseNames := map[string]map[string]bool{resClusterRoles: {}, resRoles: {}}

	for _, v := range base {
		baseNames[escalationResource(v.role)][nameOf(v.role)] = true
	}

	for label, docs := range map[string][]map[string]any{labelConfig: cfgDocs, labelChart: chartDocs} {
		// Base objects the Job does not hold in some variant: update-only, so escalate/bind by name.
		needEscalation := map[string]map[string]bool{resClusterRoles: {}, resRoles: {}}

		for _, v := range base {
			if u := uncoveredByCNIJob(docs, v.role); len(u) != 0 {
				needEscalation[escalationResource(v.role)][nameOf(v.role)] = true
			}
		}

		for _, v := range features {
			u := uncoveredByCNIJob(docs, v.role)
			if len(u) == 0 {
				continue
			}

			if baseNames[escalationResource(v.role)][nameOf(v.role)] {
				needEscalation[escalationResource(v.role)][nameOf(v.role)] = true

				continue
			}

			t.Errorf("%s: with %s on, the Cilium chart renders %s %s/%s, which the Cilium Job would create but does not hold "+
				"(escalate by name does not apply to a create): %s",
				label, v.feature, kindOf(v.role), namespaceOf(v.role), nameOf(v.role), strings.Join(u, "; "))
		}

		got := escalationNames(t, label, find(docs, kindClusterRole, cniRoleName(label)))
		for res, want := range needEscalation {
			for n := range want {
				if !got[res][n] {
					t.Errorf("%s: the Job holds too little for the base %s %q and cannot bind or escalate it by name", label, res, n)
				}
			}

			for n := range got[res] {
				if !want[n] {
					t.Errorf("%s: the CNI role binds or escalates %s %q, which it holds the rules of anyway or the chart does not "+
						"render as a base object", label, res, n)
				}
			}
		}
	}
}

func cniRoleName(label string) string {
	if label == labelChart {
		return "ichor-cni-role"
	}

	return "cni-role"
}

// escalationResource is the RBAC resource a Role or ClusterRole object is addressed as.
func escalationResource(role map[string]any) string {
	if kindOf(role) == kindClusterRole {
		return resClusterRoles
	}

	return resRoles
}
