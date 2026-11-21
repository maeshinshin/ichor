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

// codeConstant is why the harness paths may keep the names: renaming them reaches the whole harness.
const codeConstant = "the e2e namespace is a code constant there; " +
	"renaming it reaches the whole harness, so it is a separate change"

// allowed says where a match may stay. Everywhere else a match fails the test.
var allowed = []struct {
	path string
	dir  bool // path is a directory prefix, not one file
	why  string
}{
	{"test/e2e/e2e_test.go", false,
		codeConstant},
	{"test/e2e/foreign/reclaim_edges_test.go", false,
		codeConstant},
	{"test/e2e/foreign/reclaim_test.go", false,
		codeConstant},
	{"test/e2e/foreign/reclaim_ui_test.go", false,
		codeConstant},
	{"test/harness/harness.mk", false,
		codeConstant},
	{"test/harness/kubevirt/cache.go", false,
		codeConstant},
	{"test/harness/kubevirt/config_test.go", false,
		codeConstant},
	{"test/harness/kubevirt/controller.go", false,
		codeConstant},
	{"test/harness/kubevirt/ha.go", false,
		codeConstant},
	{"test/harness/kubevirt/main.go", false,
		codeConstant},
	{"test/harness/ui-demo/README.md", false,
		codeConstant},
	{"test/internal/foreign/foreign.go", false,
		codeConstant},
	{"test/internal/kube/kube.go", false,
		codeConstant},
	{"test/internal/kube/kube_test.go", false,
		codeConstant},
	// TEMPORARY, not a decision: another agent holds these two files right now. Remove these entries when that
	// agent's rename lands; the names are then checked here like everywhere else.
	{"Makefile", false,
		"TEMPORARY: held by another agent for the e2e namespace rename; not a decision"},
	{"hack/cluster-gate.sh", false,
		"TEMPORARY: held by another agent for the e2e namespace rename; not a decision"},
}

// allowedIndex returns the allowance that covers name, or -1.
func allowedIndex(name string) int {
	for i, a := range allowed {
		if a.dir && strings.HasPrefix(name, a.path) || !a.dir && name == a.path {
			return i
		}
	}

	return -1
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

	// left counts the forbidden names still found under each allowance. An allowance with none left is stale.
	left := make([]int, len(allowed))

	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" {
			continue
		}

		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue // a tracked file deleted in the working tree
		}

		if bytes.IndexByte(src, 0) >= 0 {
			continue // a binary file
		}

		hits := names.FindAllIndex(src, -1)

		if i := allowedIndex(name); i >= 0 {
			left[i] += len(hits)

			continue
		}

		for _, loc := range hits {
			line := bytes.Count(src[:loc[0]], []byte("\n")) + 1
			report = append(report, fmt.Sprintf("%s:%d: %q names the maintainer's environment", name, line, src[loc[0]:loc[1]]))
		}
	}

	for i, a := range allowed {
		if left[i] == 0 {
			report = append(report, fmt.Sprintf(
				"%s: no forbidden name is left under this allowance, so the entry can be deleted (it was: %s)",
				a.path, a.why))
		}
	}

	if len(report) > 0 {
		t.Errorf("the name check failed; rename a name to <e2e-namespace> or a generic example, "+
			"or delete a stale allowance:\n%s",
			strings.Join(report, "\n"))
	}
}
