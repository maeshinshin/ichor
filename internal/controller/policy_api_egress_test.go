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

package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Each policy the controller writes selects the controller's pods. With the chart's base policy gone it
// is the only one, an egress default-deny, so it must carry the apiserver rule the base policy has.
func TestControllerMadePoliciesKeepTheAPIServerReachable(t *testing.T) {
	const app = "ichor"

	// The five policies, built the way their writers build them.
	factory := TalosEgress{}
	specs := map[string]map[string]any{
		DefaultReleaseCheckPolicyName:     releaseCheckSpec(app, []string{testGHCR}),
		DefaultInstallerImagePolicyName:   releaseCheckSpec(app, []string{testGHCR}),
		DefaultUIEgressPolicyName:         uiEgressSpec(app, urlTarget{}),
		DefaultTalosEgressPolicyName:      {keyEgress: []any{}},
		factory.name() + "-image-factory": {keyEgress: []any{}},
	}

	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	for name, spec := range specs {
		if _, err := writePolicy(context.Background(), c, "ns", name, app, spec); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(ciliumPolicyGVK)

		if err := c.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		egress, _, _ := unstructured.NestedSlice(got.Object, keySpec, keyEgress)
		if !hasAPIServerRule(egress) {
			t.Errorf("%s: egress %v lacks toEntities kube-apiserver on 6443", name, egress)
		}
	}
}

func hasAPIServerRule(egress []any) bool {
	for _, r := range egress {
		rule, _ := r.(map[string]any)
		ents, _ := rule["toEntities"].([]any)
		tp, _ := rule[keyToPorts].([]any)

		if len(ents) == 1 && ents[0] == "kube-apiserver" && len(tp) == 1 {
			ports, _ := tp[0].(map[string]any)[keyPorts].([]any)
			if len(ports) == 1 && ports[0].(map[string]any)["port"] == "6443" {
				return true
			}
		}
	}

	return false
}
