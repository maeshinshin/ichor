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

package installer

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// crdWith builds a one-version CRD named name whose spec has the given string properties and
// whose spec.cni (an object) has the given string properties.
func crdWith(name string, spec, cni []string) *unstructured.Unstructured {
	str := func(names []string) map[string]any {
		m := map[string]any{}
		for _, n := range names {
			m[n] = map[string]any{"type": "string"}
		}

		return m
	}

	props := str(spec)
	props["cni"] = map[string]any{"type": "object", "properties": str(cni)}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"group": "ichor.maesh.dev",
			"scope": "Namespaced",
			"names": map[string]any{"kind": "X", "plural": "xs"},
			"versions": []any{map[string]any{
				"name": "v1alpha1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spec": map[string]any{"type": "object", "properties": props},
					},
				}},
			}},
		},
	}}
}

// A retired field (ADR 0101) may disappear, at the top of spec and nested, but only on its own CRD.
func TestCheckAdditiveAllowsRetiredFields(t *testing.T) {
	const clusters = "talosclusters.ichor.maesh.dev"

	live := crdWith(clusters, []string{"gitops", "keep"}, []string{"handover", "keep"})
	target := crdWith(clusters, []string{"keep"}, []string{"keep"})

	if err := checkAdditive(live, target); err != nil {
		t.Fatalf("retired spec.gitops and spec.cni.handover were refused: %v", err)
	}

	// The same path on a CRD that did not retire it is still a removal.
	other := "widgets.ichor.maesh.dev"
	if err := checkAdditive(crdWith(other, []string{"gitops"}, nil), crdWith(other, nil, nil)); err == nil {
		t.Fatal("spec.gitops was allowed to disappear from a CRD that did not retire it")
	}
}

// Every removal that is not on the list is still refused.
func TestCheckAdditiveStillRefusesOtherRemovals(t *testing.T) {
	const clusters = "talosclusters.ichor.maesh.dev"

	live := crdWith(clusters, []string{"gitops", "keep", "color"}, []string{"keep"})
	target := crdWith(clusters, []string{"keep"}, []string{"keep"})

	err := checkAdditive(live, target)
	if err == nil || !strings.Contains(err.Error(), "spec.color would be removed") {
		t.Fatalf("checkAdditive = %v, want a refusal naming spec.color", err)
	}

	live = crdWith(clusters, []string{"keep"}, []string{"keep", "other"})
	if err := checkAdditive(live, crdWith(clusters, []string{"keep"}, []string{"keep"})); err == nil {
		t.Fatal("a removal of spec.cni.other was allowed")
	}
}
