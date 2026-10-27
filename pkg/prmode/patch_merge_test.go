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

package prmode

import (
	"strings"
	"testing"
)

var mergeTestTarget = Target{
	APIVersion: "ichor.maesh.dev/v1alpha1", Kind: "TalosConfigPatch", Namespace: "ichor-system", Name: "p",
}

const mergeFixture = `# a patch
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata:
  name: p
  namespace: ichor-system
spec:
  # which cluster
  clusterRef:
    name: prod # the prod cluster
  layer: cluster
  documents:
    # tune the network
    - kind: SysctlConfig
      sysctlConfig:
        params:
          net.core.somaxconn: "4096" # raised for ingress
          vm.max_map_count: "262144"
    - kind: Other
      other:
        keep: me # untouched
`

// A merge update rewrites only what differs: comments on unchanged fields, on changed
// scalars, and on neighbours all survive.
func TestPatch_MergeKeepsCommentsOutsideChangedFields(t *testing.T) {
	spec := map[string]any{
		"clusterRef": map[string]any{"name": "prod"},
		"layer":      "cluster",
		"documents": []any{
			map[string]any{
				"kind": "SysctlConfig",
				"sysctlConfig": map[string]any{"params": map[string]any{
					"net.core.somaxconn": "8192",
					"vm.max_map_count":   "262144",
				}},
			},
			map[string]any{"kind": "Other", "other": map[string]any{"keep": "me"}},
		},
	}

	got, err := Patch([]byte(mergeFixture), mergeTestTarget, []FieldUpdate{{Path: []string{specKey}, Value: spec, Merge: true}})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	want := `# a patch
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata:
  name: p
  namespace: ichor-system
spec:
  # which cluster
  clusterRef:
    name: prod # the prod cluster
  layer: cluster
  documents:
    # tune the network
    - kind: SysctlConfig
      sysctlConfig:
        params:
          net.core.somaxconn: "8192" # raised for ingress
          vm.max_map_count: "262144"
    - kind: Other
      other:
        keep: me # untouched
`
	if string(got) != want {
		t.Fatalf("mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Keys and list items absent from the new value are removed; new ones are added.
func TestPatch_MergeAddsAndRemoves(t *testing.T) {
	spec := map[string]any{
		"clusterRef": map[string]any{"name": "prod"},
		"layer":      "cluster",
		"documents": []any{
			map[string]any{
				"kind": "SysctlConfig",
				"sysctlConfig": map[string]any{"params": map[string]any{
					"net.core.somaxconn": "4096",
					"fs.file-max":        "1000",
				}},
			},
		},
	}

	got, err := Patch([]byte(mergeFixture), mergeTestTarget, []FieldUpdate{{Path: []string{specKey}, Value: spec, Merge: true}})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	want := `# a patch
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata:
  name: p
  namespace: ichor-system
spec:
  # which cluster
  clusterRef:
    name: prod # the prod cluster
  layer: cluster
  documents:
    # tune the network
    - kind: SysctlConfig
      sysctlConfig:
        params:
          net.core.somaxconn: "4096" # raised for ingress
          fs.file-max: "1000"
`
	if string(got) != want {
		t.Fatalf("mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Without Merge the leaf is replaced as before (comments inside it go).
func TestPatch_WithoutMergeStillReplacesWholesale(t *testing.T) {
	got, err := Patch([]byte(mergeFixture), mergeTestTarget,
		[]FieldUpdate{{Path: []string{specKey}, Value: map[string]any{"layer": "cluster"}}})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if want := "spec:\n  layer: cluster\n"; !strings.Contains(string(got), want) {
		t.Fatalf("expected %q in:\n%s", want, got)
	}
}
