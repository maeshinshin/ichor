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
	"regexp"
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

// removedCommandPaths are the CLI command paths that the verb-first surface
// (b0a9823cc) retired. An ADR names the current command, so none of these may
// appear in one, as `ichor <path>` or as a `<path>` code span. A later rename adds
// its old path here.
var removedCommandPaths = []string{
	"auth kubeconfig",
	"auth status",
	"cluster bundle-export",
	"cluster credentials",
	"cluster discovery",
	"cluster discovery add",
	"cluster discovery list",
	"cluster discovery remove",
	"cluster get",
	"cluster list",
	"cluster rotate-ca",
	"cluster rotate-keys",
	"cluster shutdown",
	"cluster status",
	"cluster talosconfig",
	"cni bootstrap",
	"cni kube-proxy",
	"cni upgrade",
	"cni values",
	"cni values diff",
	"cni values edit",
	"cni values get",
	"cni values set",
	"config apply",
	"config effective",
	"etcd backup",
	"etcd backup now",
	"etcd defrag",
	"etcd forfeit-leadership",
	"etcd snapshot",
	"machine approve",
	"machine get",
	"machine list",
	"machine reclaim",
	"machine reject",
	"node get",
	"node logs",
	"node pcap",
	"node reboot",
	"node remove",
	"node restart-service",
	"node shutdown",
	"node support-bundle",
	"operation delete",
	"operation get",
	"operation list",
	"operation pause",
	"operation resume",
	"patch create",
	"patch delete",
	"patch get",
	"patch list",
	"patch preview",
	"patch update",
	"settings get",
	"settings set",
	"user create",
	"user delete",
	"user get",
	"user list",
	"user update",
}

// TestNoADRNamesARemovedCommand is the rule behind the kube-proxy fix: an ADR
// that names a command the CLI no longer has is a defect, whichever command it
// is. The decisions an ADR records are not checked, only the command spellings.
func TestNoADRNamesARemovedCommand(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(adrDir, "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("want ADRs under %s, got %v (err %v)", adrDir, paths, err)
	}
	for _, cmd := range removedCommandPaths {
		re := regexp.MustCompile("(ichor |`)" + regexp.QuoteMeta(cmd) + "([^a-z-]|$)")
		for _, path := range paths {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			if n := len(re.FindAll(body, -1)); n > 0 {
				t.Errorf("%s names the removed command %q (%d time(s))", path, cmd, n)
			}
		}
	}
}
