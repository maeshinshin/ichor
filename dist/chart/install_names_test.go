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
	"slices"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/internal/controller"
)

const (
	kustomizeInstall = "kustomize"
	verbEscalate     = "escalate"
	rbacGroup        = "rbac.authorization.k8s.io"
)

// grantedOn reports whether grants allow verb on res for name ("" = a rule that holds every name).
func grantedOn(grants map[rbacTriple]bool, res [2]string, verb, name string) bool {
	for tr := range grants {
		if tr.group != res[0] || tr.resource != res[1] || tr.verb != verb {
			continue
		}

		if tr.names == "" || (name != "" && slices.Contains(strings.Split(tr.names, ","), name)) {
			return true
		}
	}

	return false
}

// resourceOf splits "<apiGroup>/<resource>" (testdata/install_resources.yaml).
func resourceOf(s string) [2]string {
	group, resource, _ := strings.Cut(s, "/")

	return [2]string{group, resource}
}

func stringMap(v any) map[string]string {
	out := map[string]string{}

	m, _ := v.(map[string]any)
	for k, val := range m {
		out[k], _ = val.(string)
	}

	return out
}

// TestInstallerCanApplyEveryObjectOfTheInstallManifest checks, for the installer ClusterRole and Role in
// config/rbac and in the chart, that each object of dist/install.yaml can be got and created, and that
// the kind a ValidatingAdmissionPolicy names as paramKind can be got on every object (the apiserver
// refuses the policy otherwise, with "must have get permission on all objects of the
// referenced paramKind"). It needs `make build-installer`.
func TestInstallerCanApplyEveryObjectOfTheInstallManifest(t *testing.T) {
	install := loadYAMLDocs(t, "../install.yaml")
	table := loadYAMLDocs(t, "testdata/install_resources.yaml")[0]
	kinds, paramKinds := stringMap(table["kinds"]), stringMap(table["paramKinds"])

	cfg := loadYAMLDocs(t, installerConfigRolePath)
	chartDocs := renderChartWithValues(t, "ichor", "installer.enabled=true")
	grants := map[string]map[rbacTriple]bool{
		labelConfig: rbacTriples(append(allRules(find(cfg, kindClusterRole, "installer-role")),
			allRules(find(cfg, kindRole, "installer-role"))...)),
		labelChart: rbacTriples(append(allRules(find(chartDocs, kindClusterRole, "ichor-installer-role")),
			allRules(find(chartDocs, kindRole, "ichor-installer-role"))...)),
	}

	for label, g := range grants {
		for _, d := range install {
			kind := kindOf(d)
			if kind == kindSecret || kind == "Namespace" {
				continue
			}

			entry, ok := kinds[kind]
			if !ok {
				t.Errorf("%s: install.yaml holds a %s; add it to testdata/install_resources.yaml and give the "+
					"installer its rights", label, kind)

				continue
			}

			res := resourceOf(entry)

			if !grantedOn(g, res, verbGetController, nameOf(d)) {
				t.Errorf("%s: the installer cannot get %s %q", label, kind, nameOf(d))
			}

			// create cannot be limited by name: only a rule that holds every name counts.
			if !grantedOn(g, res, verbCreateController, "") {
				t.Errorf("%s: the installer cannot create %s %q", label, kind, nameOf(d))
			}

			if kind != kindValidatingAdmissionPolicy {
				continue
			}

			spec, _ := d["spec"].(map[string]any)
			pk, _ := spec["paramKind"].(map[string]any)

			if pk == nil {
				continue
			}

			pkKind, _ := pk["kind"].(string)

			if pres, known := paramKinds[pkKind]; !known {
				t.Errorf("%s: %s names paramKind %s; add it to paramKinds in testdata/install_resources.yaml",
					label, nameOf(d), pkKind)
			} else if !grantedOn(g, resourceOf(pres), verbGetController, "") {
				t.Errorf("%s: %s names paramKind %s and the installer cannot get every object of it",
					label, nameOf(d), pkKind)
			}
		}
	}
}

// TestControllerLooksUpNamesThatExist checks the names the controller reads at runtime against both install
// paths: the ClusterRole it keeps equal to the IchorUsers must exist under exactly that name (kustomize's
// namePrefix once turned it into ichor-ichor-ui-impersonate while the controller looked for
// ichor-ui-impersonate), and the controller's own role must name it for get, update and escalate.
func TestControllerLooksUpNamesThatExist(t *testing.T) {
	renders := map[string][]map[string]any{
		kustomizeInstall: loadYAMLDocs(t, "../install.yaml"),
		labelChart:       renderChartWithValues(t, "ichor", usersOn, usersURL),
	}

	for label, docs := range renders {
		name := controller.ImpersonationRoleName

		if find(docs, kindClusterRole, name) == nil {
			t.Errorf("%s: no ClusterRole %q, the name the controller keeps", label, name)
		}

		binding := find(docs, kindClusterRoleBinding, name)
		if binding == nil {
			t.Errorf("%s: no ClusterRoleBinding %q", label, name)
		} else if ref, _ := clusterRoleBindingRefs(binding); ref != name {
			t.Errorf("%s: the binding refers to ClusterRole %q, want %q", label, ref, name)
		}

		managerRole := findNameSuffix(docs, kindClusterRole, "manager-role")
		if managerRole == nil {
			t.Fatalf("%s: no manager ClusterRole", label)
		}

		triples := rbacTriples(allRules(managerRole))
		for _, verb := range []string{verbGetController, verbUpdate, verbEscalate} {
			if !triples[rbacTriple{rbacGroup, resClusterRoles, verb, name}] {
				t.Errorf("%s: the manager ClusterRole cannot %s clusterroles named %q", label, verb, name)
			}
		}
	}
}

// TestManagerPrefersAControlPlane: after an etcd restore a worker's kube-proxy is stale and the controller on
// it cannot reach the kubernetes Service to repair it, so both install paths ask the scheduler for a control
// plane (a preference, never a requirement).
func TestManagerPrefersAControlPlane(t *testing.T) {
	pods := map[string]map[string]any{
		labelChart: findNameSuffix(renderChart(t, "ichor"), kindDeployment, "controller-manager"),
	}

	for _, d := range loadYAMLDocs(t, "../../config/manager/manager.yaml") {
		if d["kind"] == kindDeployment {
			pods[labelConfig] = d
		}
	}

	for label, d := range pods {
		podSpec := podSpecOf(d)
		aff, _ := podSpec["affinity"].(map[string]any)
		na, _ := aff["nodeAffinity"].(map[string]any)

		if _, required := na["requiredDuringSchedulingIgnoredDuringExecution"]; required {
			t.Errorf("%s: the controller must not require a node kind", label)
		}

		preferred := fmt.Sprint(na["preferredDuringSchedulingIgnoredDuringExecution"])
		if !strings.Contains(preferred, "node-role.kubernetes.io/control-plane") {
			t.Errorf("%s: the controller does not prefer a control plane: %v", label, na)
		}
	}
}

// TestUIServiceExternalTrafficPolicy: the failed-login counters need the client's address, which a
// LoadBalancer Service keeps only with externalTrafficPolicy Local (docs/guides/users-and-login.md).
func TestUIServiceExternalTrafficPolicy(t *testing.T) {
	policy := func(sets ...string) any {
		svc := find(renderChartWithValues(t, "ichor", sets...), kindService, "ichor-ui-service")
		spec, _ := svc["spec"].(map[string]any)

		return spec["externalTrafficPolicy"]
	}

	if got := policy(); got != nil {
		t.Errorf("default externalTrafficPolicy = %v, want unset", got)
	}

	if got := policy("ichorUI.service.type=LoadBalancer", "ichorUI.service.externalTrafficPolicy=Local"); got != "Local" {
		t.Errorf("externalTrafficPolicy = %v, want Local", got)
	}
}
