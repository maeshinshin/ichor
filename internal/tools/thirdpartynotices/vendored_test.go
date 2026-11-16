package main

import (
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is the repository root, seen from this package.
const repoRoot = "../../.."

// TestVendoredRadixCopiesAreInNotices fails when a tracked directory holds a copy of Radix UI Primitives that
// THIRD-PARTY-NOTICES does not name, which is how a second vendored copy goes unattributed.
// shortcut: the copies carry no upstream header, so they are found by their src/lib/radix location only; widen the match when another upstream vendors under a different directory.
func TestVendoredRadixCopiesAreInNotices(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	list := exec.Command("git", "ls-files", "-z")
	list.Dir = repoRoot

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

// shimHeader matches the first-line comment a port opens with: "// cmdk 1.1.1 on Preact hooks" or "// Port of sonner 2.0.8 to Preact".
var shimHeader = regexp.MustCompile(`(?i)^// (?:port of )?([a-z][a-z0-9._-]*) v?[0-9]+\.[0-9]+\.[0-9]+\b`)

// TestShimUpstreamsAreInNotices fails when a file under src/lib/shims names the upstream it ports in its header and
// THIRD-PARTY-NOTICES does not name that upstream, which is how the cmdk port shipped unattributed.
// shortcut: it reads only a header that opens "<name> <x.y.z>" or "Port of <name> <x.y.z>" and matches the name, not the path;
// a port with no such header (next-themes.tsx has none) is invisible here, so drop this for a header every shim must carry.
// An upstream inlined inside a port is named by no header, so it needs a test of its own (TestScoringFunctionNamesSuperhuman).
func TestShimUpstreamsAreInNotices(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	list := exec.Command("git", "ls-files", "-z", "--", "ui/src/lib/shims", "site/src/lib/shims")
	list.Dir = repoRoot

	out, err := list.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	seen := 0
	for f := range strings.SplitSeq(string(out), "\x00") {
		if !strings.HasSuffix(f, ".ts") && !strings.HasSuffix(f, ".tsx") {
			continue
		}

		b, err := os.ReadFile(repoRoot + "/" + f)
		if err != nil {
			t.Fatal(err)
		}

		for _, l := range strings.SplitN(string(b), "\n", 40) {
			if m := shimHeader.FindStringSubmatch(l); m != nil {
				seen++
				if !strings.Contains(string(notices), m[1]) {
					t.Errorf("%s names %s as its upstream, which THIRD-PARTY-NOTICES does not name", f, m[1])
				}

				break
			}
		}
	}

	if seen == 0 {
		t.Fatal("no shim header names an upstream; the check has nothing to look at")
	}
}

// TestScoringFunctionNamesSuperhuman fails when a tracked script holds cmdk's scoring function and THIRD-PARTY-NOTICES
// does not name Superhuman, whose command-score it is. The shims' headers name only cmdk, so TestShimUpstreamsAreInNotices
// cannot see an upstream that is inlined inside the port; this is how command-score went unattributed.
// shortcut: SCORE_CONTINUE_MATCH is the handle, so a port that renames its constants escapes; drop this for a per-file
// list of what each shim inlines once a second inlined upstream turns up.
func TestScoringFunctionNamesSuperhuman(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	// The needle is split so that this file does not match itself.
	grep := exec.Command("git", "grep", "-l", "-z", "SCORE_"+"CONTINUE_MATCH", "--", "*.ts", "*.tsx", "*.js", "*.jsx", "*.mjs")
	grep.Dir = repoRoot

	out, err := grep.Output()
	if err != nil {
		t.Fatalf("git grep: %v", err) // exit 1 (no match) lands here too: the check would have nothing to look at
	}

	if !strings.Contains(string(notices), "Superhuman") {
		for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
			t.Errorf("%s holds the scoring function of Superhuman's command-score, which THIRD-PARTY-NOTICES does not name", f)
		}
	}
}

// TestComponentCopiesAreInNotices fails when a tracked directory holds a copy of the shadcn/ui component ports that
// THIRD-PARTY-NOTICES does not name, which is how site/src/components/ui shipped unnamed beside ui/src/components/ui.
// shortcut: the copies carry no upstream header, and data-slot also marks a file that is not vendored
// (ui/src/components/NodeLogs.tsx), so they are found by their components/ui location only; widen the match when another copy lives elsewhere.
func TestComponentCopiesAreInNotices(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	list := exec.Command("git", "ls-files", "-z")
	list.Dir = repoRoot

	out, err := list.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	dirs := map[string]bool{}
	for f := range strings.SplitSeq(string(out), "\x00") {
		if d := path.Dir(f); strings.HasSuffix(d, "/src/components/ui") {
			dirs[d] = true
		}
	}

	if len(dirs) == 0 {
		t.Fatal("no src/components/ui directory is tracked; the check has nothing to look at")
	}

	for d := range dirs {
		if !strings.Contains(string(notices), d) {
			t.Errorf("%s is a vendored copy of the shadcn/ui component ports, which THIRD-PARTY-NOTICES does not name", d)
		}
	}
}

// namesCopiesOf fails when a tracked TypeScript file holding needle is not named by its full path in THIRD-PARTY-NOTICES,
// which is how the site's copies of sonner and next-themes went unnamed beside the UI's.
// shortcut: the copies are found by a string only they hold, so a copy that renames it escapes; drop this for a per-file list once a copy turns up that holds none.
func namesCopiesOf(t *testing.T, needle string) {
	t.Helper()

	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	grep := exec.Command("git", "grep", "-l", "-z", needle, "--", "*.ts", "*.tsx")
	grep.Dir = repoRoot

	out, err := grep.Output()
	if err != nil {
		t.Fatalf("git grep: %v", err) // exit 1 (no match) lands here too: the check would have nothing to look at
	}

	for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		if !strings.Contains(string(notices), f) {
			t.Errorf("%s holds a copy of an upstream, which THIRD-PARTY-NOTICES does not name", f)
		}
	}
}

// TestSonnerCopiesAreInNotices fails when a copy of sonner's shim or its styles is not named by path.
func TestSonnerCopiesAreInNotices(t *testing.T) {
	namesCopiesOf(t, "data-sonner-toaster")
}

// TestNextThemesCopiesAreInNotices fails when a copy of the next-themes shim is not named by path.
func TestNextThemesCopiesAreInNotices(t *testing.T) {
	namesCopiesOf(t, "UseThemeProps")
}
