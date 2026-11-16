package main

import (
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
)

// TestVendoredRadixCopiesAreInNotices fails when a tracked directory holds a copy of Radix UI Primitives that
// THIRD-PARTY-NOTICES does not name, which is how a second vendored copy goes unattributed.
// shortcut: the copies carry no upstream header, so they are found by their src/lib/radix location only; widen the match when another upstream vendors under a different directory.
func TestVendoredRadixCopiesAreInNotices(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	list := exec.Command("git", "ls-files", "-z")
	list.Dir = "../../.."

	out, err := list.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	dirs := map[string]bool{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		if d := path.Dir(f); strings.HasSuffix(d, "/src/lib/radix") {
			dirs[d] = true
		}
	}

	if len(dirs) == 0 {
		t.Fatal("no src/lib/radix directory is tracked; the check has nothing to look at")
	}

	for d := range dirs {
		if !strings.Contains(string(notices), d) {
			t.Errorf("%s is a vendored copy of Radix UI Primitives, which THIRD-PARTY-NOTICES does not name", d)
		}
	}
}
