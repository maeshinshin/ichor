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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adrDir is the real repo directory this command generates from.
const adrDir = "../../../docs/adr"

// TestGenerateIndexIsCurrent is the generated-artifact drift check for
// docs/adr/README.md: regenerating it against the real docs/adr/ changes
// nothing, which is only true if every ADR already has the row it needs
// and no stray row exists for one that doesn't -- the exact pair of
// failures docs/adr/index_test.go's TestIndexMatchesFiles used to catch
// by hand. That test is gone; this is its replacement, now that the file
// it was checking is generated instead of hand-maintained.
func TestGenerateIndexIsCurrent(t *testing.T) {
	path := filepath.Join(adrDir, "README.md")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	if err := generateIndex(adrDir); err != nil {
		t.Fatalf("generateIndex: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	if string(before) != string(after) {
		t.Fatalf("%s is stale; run `make adr-index` and commit the result", path)
	}
}

// TestParseADRsIsDeterministic proves the same files always produce the
// same entries in the same order, regardless of os.ReadDir's own
// ordering guarantees.
func TestParseADRsIsDeterministic(t *testing.T) {
	first, err := parseADRs(adrDir)
	if err != nil {
		t.Fatalf("parseADRs: %v", err)
	}

	second, err := parseADRs(adrDir)
	if err != nil {
		t.Fatalf("parseADRs: %v", err)
	}

	if len(first) == 0 {
		t.Fatal("parsed zero ADRs")
	}

	if len(first) != len(second) {
		t.Fatalf("parseADRs returned %d entries, then %d", len(first), len(second))
	}

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("entry %d differs between runs: %+v vs %+v", i, first[i], second[i])
		}

		if i > 0 && first[i-1].Number >= first[i].Number {
			t.Fatalf("entries not sorted ascending: %d then %d", first[i-1].Number, first[i].Number)
		}
	}
}

// TestADR0066NamesTheCommandThatExists pins the command the kube-proxy switch
// is run with: the CLI registers `ichor set kube-proxy` (internal/cli/cni.go),
// and ADR 0066 once named `ichor cni kube-proxy`, which does not exist.
func TestADR0066NamesTheCommandThatExists(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(adrDir, "0066-*.md"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("want exactly one ADR 0066 file, got %v (err %v)", paths, err)
	}
	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("reading %s: %v", paths[0], err)
	}
	if strings.Contains(string(body), "ichor cni kube-proxy") {
		t.Errorf("%s names `ichor cni kube-proxy`, which is not a command; use `ichor set kube-proxy`", paths[0])
	}
	if !strings.Contains(string(body), "ichor set kube-proxy") {
		t.Errorf("%s does not name `ichor set kube-proxy`, the command that exists", paths[0])
	}
}
