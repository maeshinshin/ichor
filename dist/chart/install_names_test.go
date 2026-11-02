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
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/internal/controller"
)

// installResources maps every kind of dist/install.yaml the installer applies to its (apiGroup, resource).
// Secret is skipped by the installer (internal/installer/manifest.go) and the Namespace exists before the
// installer runs, so neither is listed. A new kind fails the test below until it is added here.
var installResources = map[string][2]string{
	"MutatingWebhookConfiguration":     {"admissionregistration.k8s.io", "mutatingwebhookconfigurations"},
	"ValidatingWebhookConfiguration":   {"admissionregistration.k8s.io", "validatingwebhookconfigurations"},
	"ValidatingAdmissionPolicy":        {"admissionregistration.k8s.io", "validatingadmissionpolicies"},
	"ValidatingAdmissionPolicyBinding": {"admissionregistration.k8s.io", "validatingadmissionpolicybindings"},
	"CustomResourceDefinition":         {"apiextensions.k8s.io", "customresourcedefinitions"},
	"Deployment":                       {"apps", "deployments"},
	"ClusterRole":                      {"rbac.authorization.k8s.io", "clusterroles"},
	"ClusterRoleBinding":               {"rbac.authorization.k8s.io", "clusterrolebindings"},
	"Role":                             {"rbac.authorization.k8s.io", "roles"},
	"RoleBinding":                      {"rbac.authorization.k8s.io", "rolebindings"},
	"PriorityClass":                    {"scheduling.k8s.io", "priorityclasses"},
	"Service":                          {"", "services"},
	"ServiceAccount":                   {"", "serviceaccounts"},
}

// grantedOn reports whether grants allow verb on res for name ("" = a rule that holds every name).
func grantedOn(grants map[rbacTriple]bool, res [2]string, verb, name string) bool {
	for tr := range grants {
		if tr.group != res[0] || tr.resource != res[1] || tr.verb != verb {
			continue
		}

		if tr.names == "" {
			return true
		}

		for _, n := range strings.Split(tr.names, ",") {
			if name != "" && n == name {
				return true
			}
		}
	}

	return false
}

// TestInstallerCanApplyEveryObjectOfTheInstallManifest checks, for the installer ClusterRole and Role in
// config/rbac and in the chart, that each object of dist/install.yaml can be got and created, and that
// the kind a ValidatingAdmissionPolicy names as paramKind can be got on every object (the apiserver
// refuses the policy otherwise; seen on hardware as "must have get permission on all objects of the
// referenced paramKind"). It needs `make build-installer`.
func TestInstallerCanApplyEveryObjectOfTheInstallManifest(t *testing.T) {
	install := loadYAMLDocs(t, "../install.yaml")
	paramResources := map[string][2]string{"IchorInstallation": {"ichor.maesh.dev", "ichorinstallations"}}

	cfg := loadYAMLDocs(t, installerConfigRolePath)
	chartDocs := renderChartWithValues(t, "ichor", "installer.enabled=true")
	grants := map[string]map[rbacTriple]bool{
		labelConfig: rbacTriples(append(allRules(find(cfg, kindClusterRole, "installer-role")), allRules(find(cfg, kindRole, "installer-role"))...)),
		labelChart:  rbacTriples(append(allRules(find(chartDocs, kindClusterRole, "ichor-installer-role")), allRules(find(chartDocs, kindRole, "ichor-installer-role"))...)),
	}

	for label, g := range grants {
		for _, d := range install {
			kind := kindOf(d)
			if kind == kindSecret || kind == "Namespace" {
				continue
			}

			res, ok := installResources[kind]
			if !ok {
				t.Errorf("%s: install.yaml holds a %s; add it to installResources and give the installer its rights", label, kind)

				continue
			}

			if !grantedOn(g, res, "get", nameOf(d)) {
				t.Errorf("%s: the installer cannot get %s %q", label, kind, nameOf(d))
			}

			// create cannot be limited by name: only a rule that holds every name counts.
			if !grantedOn(g, res, "create", "") {
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

			pres, known := paramResources[pkKind]
			if !known {
				t.Errorf("%s: %s names paramKind %s; add it to paramResources", label, nameOf(d), pkKind)
			} else if !grantedOn(g, pres, "get", "") {
				t.Errorf("%s: %s names paramKind %s and the installer cannot get every object of it", label, nameOf(d), pkKind)
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
		"kustomize": loadYAMLDocs(t, "../install.yaml"),
		"chart":     renderChartWithValues(t, "ichor", "ichorUI.users.enabled=true", "ichorUI.users.externalURL=https://x"),
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
		for _, verb := range []string{"get", "update", "escalate"} {
			if !triples[rbacTriple{"rbac.authorization.k8s.io", "clusterroles", verb, name}] {
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
		"chart": findNameSuffix(renderChart(t, "ichor"), kindDeployment, "controller-manager"),
	}

	for _, d := range loadYAMLDocs(t, "../../config/manager/manager.yaml") {
		if d["kind"] == kindDeployment {
			pods["config/manager"] = d
		}
	}

	for label, d := range pods {
		spec, _ := d["spec"].(map[string]any)
		tmpl, _ := spec["template"].(map[string]any)
		podSpec, _ := tmpl["spec"].(map[string]any)
		aff, _ := podSpec["affinity"].(map[string]any)
		na, _ := aff["nodeAffinity"].(map[string]any)

		if _, required := na["requiredDuringSchedulingIgnoredDuringExecution"]; required {
			t.Errorf("%s: the controller must not require a node kind", label)
		}

		if !strings.Contains(fmt.Sprint(na["preferredDuringSchedulingIgnoredDuringExecution"]), "node-role.kubernetes.io/control-plane") {
			t.Errorf("%s: the controller does not prefer a control plane: %v", label, na)
		}
	}
}
