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
	"strings"
	"testing"
)

// kustomizeInstallPath is dist/install.yaml, written by make build-installer from config/installer
// (the Makefile's test target runs that first).
const kustomizeInstallPath = "../../dist/install.yaml"

// TestRoleBindingSubjectsExist checks the class of bug where a RoleBinding or ClusterRoleBinding
// names a ServiceAccount that the same render does not contain: an unprefixed name, or the wrong
// namespace. kustomize's name-reference rewriting misses some kube-system bindings (see
// config/rbac/ichor-cni/controller_role.yaml), so the install manifest needs this check most.
// It runs over the kustomize install manifest and over the chart under every value set the
// other chart tests use.
func TestRoleBindingSubjectsExist(t *testing.T) {
	type render struct {
		label string
		docs  []map[string]any
	}

	valueSets := [][]string{
		nil,
		{"installer.enabled=true"},
		{"cniHelm.enabled=false"},
		{"rbac.helpers.enabled=true"},
		{"ichorUI.users.enabled=true", "ichorUI.users.externalURL=https://x"},
		{"ichorUI.credentials.serviceAccount=true"},
		{"ichorUI.diagnostics.serviceAccount=true"},
	}

	renders := make([]render, 0, 1+len(valueSets))
	renders = append(renders, render{"kustomize " + kustomizeInstallPath, loadYAMLDocs(t, kustomizeInstallPath)})

	for _, sets := range valueSets {
		label := "helm template ichor ."
		if len(sets) > 0 {
			label += " --set " + strings.Join(sets, " --set ")
		}
		renders = append(renders, render{label, renderChartWithValues(t, "ichor", sets...)})
	}

	for _, r := range renders {
		t.Run(r.label, func(t *testing.T) {
			checkRoleBindingSubjects(t, r.label, r.docs)
		})
	}
}

// checkRoleBindingSubjects fails for every ServiceAccount subject of a (Cluster)RoleBinding in
// docs that has no ServiceAccount with the same name and namespace in docs.
func checkRoleBindingSubjects(t *testing.T, label string, docs []map[string]any) {
	t.Helper()

	accounts := make(map[string]bool)
	for _, sa := range findAll(docs, kindServiceAccount) {
		accounts[namespaceOf(sa)+"/"+nameOf(sa)] = true
	}

	bindings := append(findAll(docs, kindRoleBinding), findAll(docs, kindClusterRoleBinding)...)
	for _, b := range bindings {
		subjects, _ := b["subjects"].([]any)
		for _, raw := range subjects {
			s, _ := raw.(map[string]any)
			if s["kind"] != kindServiceAccount {
				continue
			}

			ns, _ := s["namespace"].(string)
			name, _ := s["name"].(string)
			if !accounts[ns+"/"+name] {
				t.Errorf("%s: %s %q binds ServiceAccount %s/%s, which this render does not contain",
					label, kindOf(b), nameOf(b), ns, name)
			}
		}
	}
}
