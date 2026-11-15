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

const (
	repoRoot = "../../.."
	self     = "test/harness/labnames/labnames_test.go"
)

// names are the maintainer's own cluster and namespace names. The separator in the first form may be a line
// break with comment markers, so a name wrapped across two lines still matches.
var names = regexp.MustCompile(`ichor-[\s#/>*]*e2e|talos-cluster01|\bhomelab\b`)

// allowance is how many matches a tracked file may still carry, and why it is not renamed yet.
type allowance struct {
	n   int
	why string
}

// Why a listed file still carries its names. Each one is the reason its entry stays until the file changes.
const (
	harnessCode   = "the e2e harness names its namespace in code; the rename spans the harness, so it is the maintainer's call"
	heldElsewhere = "the Makefile and hack/ are held by another work item for this rename"
	remoteGate    = "hack/remote-gate.sh: another work item owns this file; its kubectl context is the cluster name"
)

// shortcut: a ratchet, not an exemption. Each listed file may carry exactly n matches: a new file fails, a file
// that gains one fails, and a file that loses one must lower its n. Drop an entry when its file is renamed.
var allowed = map[string]allowance{
	"hack/cluster-gate.sh":                    {1, heldElsewhere},
	"hack/remote-gate.sh":                     {3, remoteGate},
	"Makefile":                                {1, heldElsewhere},
	"test/e2e/e2e_test.go":                    {2, harnessCode},
	"test/e2e/foreign/foreign_test.go":        {1, harnessCode},
	"test/e2e/foreign/reclaim_edges_test.go":  {1, harnessCode},
	"test/e2e/foreign/reclaim_test.go":        {12, harnessCode},
	"test/e2e/foreign/reclaim_ui_test.go":     {3, harnessCode},
	"test/harness/harness.mk":                 {3, harnessCode},
	"test/harness/kubevirt/cache.go":          {2, harnessCode},
	"test/harness/kubevirt/config_test.go":    {2, harnessCode},
	"test/harness/kubevirt/controller.go":     {4, harnessCode},
	"test/harness/kubevirt/demo.go":           {1, harnessCode},
	"test/harness/kubevirt/ha.go":             {1, harnessCode},
	"test/harness/kubevirt/main.go":           {7, harnessCode},
	"test/harness/talosq/main.go":             {1, harnessCode},
	"test/harness/ui-demo/outer-service.yaml": {2, harnessCode},
	"test/harness/ui-demo/README.md":          {17, harnessCode},
	"test/harness/ui-demo/registry.yaml":      {3, harnessCode},
	"test/internal/foreign/foreign.go":        {8, harnessCode},
	"test/internal/kube/kube.go":              {2, harnessCode},
}

// TestNoMaintainerNamesInTheRepository fails when a tracked file names the maintainer's own cluster or namespace
// (ichor-e2e, ichor-e2e-*, talos-cluster01, homelab). An ADR records a decision, not whose machine ran it.
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

	got := map[string]int{}

	var report []string

	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" || name == self {
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
			got[name]++

			if a, ok := allowed[name]; !ok || got[name] > a.n {
				line := bytes.Count(src[:loc[0]], []byte("\n")) + 1
				report = append(report, fmt.Sprintf("%s:%d: %q names the maintainer's environment", name, line, src[loc[0]:loc[1]]))
			}
		}
	}

	// A listed file whose count moved, in either direction, is a failure: the list must match the repository.
	for name, a := range allowed {
		if got[name] != a.n {
			report = append(report, fmt.Sprintf("%s: %d matches, its allowance is %d; set the allowance to %d (%s)", name, got[name], a.n, got[name], a.why))
		}
	}

	if len(report) > 0 {
		t.Errorf("the repository names the maintainer's cluster again; rename it to <e2e-namespace> or a generic example:\n%s",
			strings.Join(report, "\n"))
	}
}
