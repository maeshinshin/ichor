// Package legacycheck keeps the word "legacy" from naming the Talos v1alpha1 machine configuration
// document. The maintainer's rule: say "the v1alpha1 document" or "Config", which is what the mirror
// calls the type. Other senses of the word (service-account tokens, a retired RPC, an upstream name)
// do not match, because the pattern needs the document words after it.
package legacycheck

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The separator class lets a phrase wrap across lines of a comment or a Markdown paragraph.
var phrase = regexp.MustCompile("(?i)\\blegacy[\\s/#*>|`-]+(document|config|machine config|v1alpha1|form|block)\\b")

// generated holds files whose text comes from upstream or from a generator; their source is checked instead.
func generated(name string) bool {
	return strings.HasPrefix(name, "docs/reference/") || strings.HasPrefix(name, "vendor/") ||
		strings.HasPrefix(name, "gen/") || strings.Contains(name, "zz_generated") ||
		strings.HasSuffix(name, "package-lock.json") || strings.HasPrefix(name, "internal/tools/legacycheck/")
}

func find(text []byte) []int {
	lines := make([]int, 0, 1)
	for _, loc := range phrase.FindAllIndex(text, -1) {
		lines = append(lines, 1+bytes.Count(text[:loc[0]], []byte("\n")))
	}
	return lines
}

func TestPhraseMatchesTheForbiddenSense(t *testing.T) {
	for _, s := range []string{"the legacy document", "Legacy `Config`", "a legacy v1alpha1 Config", "legacy\n// form", "legacy machine config"} {
		if len(find([]byte(s))) == 0 {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range []string{"legacy token Secret", "the legacy RPC", "legacy browsers", "legacy behavior"} {
		if len(find([]byte(s))) != 0 {
			t.Errorf("%q should not match", s)
		}
	}
}

// TestRepositoryDoesNotCallTheV1alpha1DocumentLegacy reads every tracked text file.
func TestRepositoryDoesNotCallTheV1alpha1DocumentLegacy(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("finding the repository root: %v", err)
	}
	root := strings.TrimSpace(string(top))
	ls := exec.Command("git", "ls-files", "-z")
	ls.Dir = root
	out, err := ls.Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name == "" || generated(name) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || bytes.IndexByte(src, 0) >= 0 { // a deleted or binary file
			continue
		}
		for _, line := range find(src) {
			t.Errorf("%s:%d: say \"the v1alpha1 document\" or \"Config\", not \"legacy\"", name, line)
		}
	}
}
