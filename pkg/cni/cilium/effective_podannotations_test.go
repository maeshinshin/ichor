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

package cilium

import (
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
)

const podAnnKey = "podAnnotations"

// TestEffectiveValuesNullPodAnnotations: a user value that is not a map for podAnnotations (null, a string, a list)
// must not panic; replaced mode still carries Ichor's annotation and enabled mode carries none.
func TestEffectiveValuesNullPodAnnotations(t *testing.T) {
	for name, bad := range map[string]any{"null": nil, "string": "x", "list": []any{"a"}, "number": 1.0} {
		t.Run(name, func(t *testing.T) {
			eff, err := EffectiveValues(map[string]any{podAnnKey: bad}, cni.KubeProxyReplaced, "", 1)
			if err != nil {
				t.Fatalf("replaced: %v", err)
			}

			pa, _ := eff[podAnnKey].(map[string]any)
			if pa[kubeProxyModeAnnotation] != "replaced" {
				t.Fatalf("replaced: podAnnotations = %v, want %s=replaced", eff[podAnnKey], kubeProxyModeAnnotation)
			}

			eff, err = EffectiveValues(map[string]any{podAnnKey: bad}, cni.KubeProxyEnabled, "", 1)
			if err != nil {
				t.Fatalf("enabled: %v", err)
			}

			if pa, _ := eff[podAnnKey].(map[string]any); pa[kubeProxyModeAnnotation] != nil {
				t.Fatalf("enabled: unexpected annotation in %v", pa)
			}
		})
	}
}

func TestEffectiveValuesPodAnnotationsKeepsUser(t *testing.T) {
	eff, err := EffectiveValues(map[string]any{podAnnKey: map[string]any{"a": "b"}}, cni.KubeProxyReplaced, "", 1)
	if err != nil {
		t.Fatal(err)
	}

	pa, _ := eff[podAnnKey].(map[string]any)
	if pa["a"] != "b" || pa[kubeProxyModeAnnotation] != "replaced" {
		t.Fatalf("podAnnotations = %v, want a=b and %s=replaced", pa, kubeProxyModeAnnotation)
	}
}
