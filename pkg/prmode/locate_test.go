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
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

const configMapYAML = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n"

const locateTargetYAML = `apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosCluster
metadata:
  name: prod
  namespace: ichor-system
spec:
  foo: bar
`

// TestLocate_FindsFileAmongSiblings proves Locate walks the whole
// directory tree under root/sourcePath, not just its top level, and
// skips files that are not Target.
func TestLocate_FindsFileAmongSiblings(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, testClustersProdPath, "other.yaml"), configMapYAML)
	writeFile(t, filepath.Join(root, testClustersProdPath, "nested", "cluster.yaml"), locateTargetYAML)

	got, err := Locate(root, testClustersProdPath, patchTestTarget)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	want := filepath.Join(root, testClustersProdPath, "nested", "cluster.yaml")
	if got != want {
		t.Fatalf("Locate = %q, want %q", got, want)
	}
}

// TestLocate_MultiDocFile proves Locate looks inside a `---`-delimited
// file's every document, not just its first.
func TestLocate_MultiDocFile(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "all.yaml"), configMapYAML+"---\n"+locateTargetYAML)

	got, err := Locate(root, "", patchTestTarget)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}

	if got != filepath.Join(root, "all.yaml") {
		t.Fatalf("Locate = %q, want all.yaml", got)
	}
}

// TestLocate_NotFoundFailsLoudly proves a target absent from every file
// under the path is an error, not an empty string.
func TestLocate_NotFoundFailsLoudly(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "other.yaml"), configMapYAML)

	if _, err := Locate(root, "", patchTestTarget); err == nil {
		t.Fatal("expected an error for a target present nowhere, got nil")
	}
}

// TestLocate_MissingDirectoryFailsLoudly proves a sourcePath that
// doesn't exist under root is an error, not an empty (so vacuously
// "not found") walk.
func TestLocate_MissingDirectoryFailsLoudly(t *testing.T) {
	root := t.TempDir()

	if _, err := Locate(root, "does-not-exist", patchTestTarget); err == nil {
		t.Fatal("expected an error for a missing directory, got nil")
	}
}
