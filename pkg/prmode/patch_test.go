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
	"testing"
)

// testClustersProdPath is the Argo CD source path fixture shared with
// locate_test.go/source_test.go.
const testClustersProdPath = "clusters/prod"

var patchTestTarget = Target{
	APIVersion: "ichor.maesh.dev/v1alpha1", Kind: "TalosCluster", Namespace: "ichor-system", Name: "prod",
}

// talosVersionPath is the field path every test below edits.
var talosVersionPath = []string{specKey, "talosVersion"}

// bumpTalosVersion is the one-field update every test below but
// TestPatch_MultipleUpdatesAtOnce makes: v1.9.0 (or '-quoted/4-space-
// indented equivalents) to v1.10.0.
var bumpTalosVersion = []FieldUpdate{{Path: talosVersionPath, Value: "v1.10.0"}}

// TestPatch_PreservesCommentsAndKeyOrder proves a scalar field change
// leaves every comment (file-level, inline, and one attached to the
// changed key itself) and every key's original order exactly as they
// were -- the thing a naive unmarshal-to-struct-then-marshal round trip
// cannot do, which is this whole package's reason to exist.
func TestPatch_PreservesCommentsAndKeyOrder(t *testing.T) {
	input := `# cluster manifest
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0 # pinned for now
  kubernetesVersion: v1.33.0
  cni:
    preset: cilium
`
	want := `# cluster manifest
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0 # pinned for now
  kubernetesVersion: v1.33.0
  cni:
    preset: cilium
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_MultiDocOnlyRightDocumentChanges proves a file holding more
// than one CR, separated by "---", has only the matching document's
// bytes touched -- the sibling document (and the separator itself) stay
// byte-for-byte identical.
func TestPatch_MultiDocOnlyRightDocumentChanges(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosMachine
metadata:
  name: node-a
  namespace: ichor-system
spec:
  approvalState: pending
---
# the target
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosMachine
metadata:
  name: node-a
  namespace: ichor-system
spec:
  approvalState: pending
---
# the target
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_PreservesAnchorsAndAliases proves an edit to one field
// leaves an unrelated anchor/alias pair in the same document intact --
// both the anchor definition and the place that references it.
func TestPatch_PreservesAnchorsAndAliases(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
  common: &common
    retries: 3
  fallback: *common
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0
  common: &common
    retries: 3
  fallback: *common
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_PreservesFourSpaceIndent proves the file's own indent width
// (here 4, not go-yaml's own default of the same width this package
// happens to also use elsewhere) is detected and kept, not reset.
func TestPatch_PreservesFourSpaceIndent(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
    name: prod
    namespace: ichor-system
spec:
    talosVersion: v1.9.0
    cni:
        preset: cilium
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
    name: prod
    namespace: ichor-system
spec:
    talosVersion: v1.10.0
    cni:
        preset: cilium
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_PreservesBlockAndFlowLists proves both of YAML's two list
// styles survive an edit to an unrelated field in the same document.
func TestPatch_PreservesBlockAndFlowLists(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
  flowList: [a, b, c]
  blockList:
    - d
    - e
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0
  flowList: [a, b, c]
  blockList:
    - d
    - e
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_QuoteStylePreserved proves a field that keeps the same kind
// of value (a short string) keeps its original quote style rather than
// falling back to go-yaml's own default when re-rendered.
func TestPatch_QuoteStylePreserved(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: 'v1.9.0'
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: 'v1.10.0'
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_TargetNotInFileFailsLoudly proves a file that simply does
// not declare target errors instead of silently returning the input
// unchanged.
func TestPatch_TargetNotInFileFailsLoudly(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: other-cluster
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
`

	_, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err == nil {
		t.Fatal("expected an error for a target not present in the file, got nil")
	}
}

// TestPatch_MultipleUpdatesAtOnce proves more than one field can change
// in a single Patch call, atomically.
func TestPatch_MultipleUpdatesAtOnce(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.9.0
  kubernetesVersion: v1.32.0
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0
  kubernetesVersion: v1.33.0
`

	got, err := Patch([]byte(input), patchTestTarget, []FieldUpdate{
		{Path: talosVersionPath, Value: "v1.10.0"},
		{Path: []string{specKey, "kubernetesVersion"}, Value: "v1.33.0"},
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestPatch_BlankLineBetweenKeysIsDropped documents the one known
// limitation (Patch's own doc comment): go-yaml's Node round trip does
// not preserve a blank line that has no comment on it, between two
// sibling keys, inside the document actually being edited. This test
// exists so that behavior is a recorded, intentional fact -- if a future
// go-yaml upgrade starts preserving it, this test will fail and should
// be updated to expect the blank line, not treated as evidence of a
// regression.
func TestPatch_BlankLineBetweenKeysIsDropped(t *testing.T) {
	input := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system

spec:
  talosVersion: v1.9.0
`
	want := `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  talosVersion: v1.10.0
`

	got, err := Patch([]byte(input), patchTestTarget, bumpTalosVersion)
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if string(got) != want {
		t.Fatalf("Patch output mismatch (expected the blank line to be dropped).\ngot:\n%s\nwant:\n%s", got, want)
	}
}
