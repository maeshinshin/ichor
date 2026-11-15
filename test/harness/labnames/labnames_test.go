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

package labnames

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const repoRoot = "../../.."

// The forbidden names are assembled from pieces: written out whole, they would match this file, which is tracked.
// The first form matches a name split by a line break with comment markers in between.
var names = regexp.MustCompile(
	"ichor-" + `[\s#/>*]*e2e` + "|" + "talos-cluster" + "01" + "|" + `\b` + "home" + "lab" + `\b`)

// allowed says where a match may stay. Everywhere else a match fails the test.
var allowed = []struct {
	path string
	dir  bool // path is a directory prefix, not one file
	why  string
}{
	{"test/e2e/", true,
		"the e2e namespace is a code constant there; renaming it reaches the whole harness, so it is a separate change"},
	{"test/harness/", true,
		"the e2e namespace is a code constant there; renaming it reaches the whole harness, so it is a separate change"},
	{"test/internal/", true,
		"the e2e namespace is a code constant there; renaming it reaches the whole harness, so it is a separate change"},
	{"hack/remote-gate.sh", false,
		"owned by another backlog item, which removes the names from this file"},
	// TEMPORARY, not a decision: another agent holds these two files right now. Remove these entries when that
	// agent's rename lands; the names are then checked here like everywhere else.
	{"Makefile", false,
		"TEMPORARY: held by another agent for the e2e namespace rename; not a decision"},
	{"hack/cluster-gate.sh", false,
		"TEMPORARY: held by another agent for the e2e namespace rename; not a decision"},
}

func isAllowed(name string) bool {
	for _, a := range allowed {
		if a.dir && strings.HasPrefix(name, a.path) || !a.dir && name == a.path {
			return true
		}
	}

	return false
}

// TestNoMaintainerNamesInTheRepository fails when a tracked file outside the allowed paths names the maintainer's own
// cluster or namespace. An ADR records a decision, not whose machine ran it.
func TestNoMaintainerNamesInTheRepository(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	ls := exec.Command("git", "ls-files", "-z")
	ls.Dir = root

	out, err := ls.Output()
	if err != nil {
		t.Fatal(err)
	}

	var report []string

	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" || isAllowed(name) {
			continue
		}

		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue // a tracked file deleted in the working tree
		}

		if bytes.IndexByte(src, 0) >= 0 {
			continue // a binary file
		}

		for _, loc := range names.FindAllIndex(src, -1) {
			line := bytes.Count(src[:loc[0]], []byte("\n")) + 1
			report = append(report, fmt.Sprintf("%s:%d: %q names the maintainer's environment", name, line, src[loc[0]:loc[1]]))
		}
	}

	if len(report) > 0 {
		t.Errorf("the repository names the maintainer's cluster again; rename it to <e2e-namespace> or a generic example:\n%s",
			strings.Join(report, "\n"))
	}
}
