package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repository root, relative to this package.
const repoRoot = "../../.."

// TestCommittedReferenceIsUpToDate regenerates the reference into a temporary directory and
// compares it with docs/reference: a change to a command, a flag or a CRD schema that was not
// followed by "make docs-reference" fails here.
func TestCommittedReferenceIsUpToDate(t *testing.T) {
	out := t.TempDir()

	if err := generate(filepath.Join(repoRoot, crdDir), out); err != nil {
		t.Fatalf("generate: %v", err)
	}

	committed := filepath.Join(repoRoot, outputDir)

	for _, sub := range []string{"cli", "crds"} {
		want := readTree(t, filepath.Join(out, sub))
		got := readTree(t, filepath.Join(committed, sub))

		if len(want) == 0 {
			t.Fatalf("%s: nothing generated", sub)
		}

		for name, w := range want {
			g, ok := got[name]
			switch {
			case !ok:
				t.Errorf("docs/reference/%s/%s is missing; run make docs-reference", sub, name)
			case !bytes.Equal(g, w):
				t.Errorf("docs/reference/%s/%s is out of date; run make docs-reference", sub, name)
			}
		}

		for name := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("docs/reference/%s/%s is no longer generated; run make docs-reference", sub, name)
			}
		}
	}
}

// TestPagesHaveFrontmatter checks every generated page opens with the title and description the
// docs site needs.
func TestPagesHaveFrontmatter(t *testing.T) {
	out := t.TempDir()

	if err := generate(filepath.Join(repoRoot, crdDir), out); err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, sub := range []string{"cli", "crds"} {
		for name, b := range readTree(t, filepath.Join(out, sub)) {
			head, _, _ := strings.Cut(string(b), "\n---\n")
			if !strings.HasPrefix(head, "---\ntitle: ") || !strings.Contains(head, "\ndescription: ") {
				t.Errorf("%s/%s has no title and description frontmatter", sub, name)
			}
		}
	}
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	files := map[string][]byte{}

	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}

		files[e.Name()] = b
	}

	return files
}
