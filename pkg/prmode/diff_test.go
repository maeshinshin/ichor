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

// TestDiff_UnifiedFormat proves Diff names both files and the changed
// line clearly enough to apply by hand (architecture.md's requirement
// for a Helm or Jsonnet source).
func TestDiff_UnifiedFormat(t *testing.T) {
	old := "spec:\n  talosVersion: v1.9.0\n"
	new := "spec:\n  talosVersion: v1.10.0\n"

	out, err := Diff(old, new, "talos-cluster.yaml")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	for _, want := range []string{"talos-cluster.yaml", "-  talosVersion: v1.9.0", "+  talosVersion: v1.10.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff output missing %q, got:\n%s", want, out)
		}
	}
}

// TestDiff_NoChange proves two identical renderings produce no hunks.
func TestDiff_NoChange(t *testing.T) {
	same := "spec:\n  talosVersion: v1.9.0\n"

	out, err := Diff(same, same, "talos-cluster.yaml")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	if out != "" {
		t.Fatalf("expected no diff for identical input, got:\n%s", out)
	}
}
