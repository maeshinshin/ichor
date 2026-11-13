package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/internal/cli"
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

// The root page lists every available top-level command once, under its help group, in the group order of
// `ichor --help`, and the groups come from the CLI's own definition.
func TestRootPageIsGroupedLikeTheHelp(t *testing.T) {
	dir := t.TempDir()
	if err := generateCLI(dir); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "ichor.md"))
	if err != nil {
		t.Fatal(err)
	}

	page := string(b)
	last := strings.Index(page, "### SEE ALSO")

	for _, g := range cli.HelpGroups() {
		heading := "#### " + strings.TrimSuffix(g.Title, ":") + "\n"

		at := strings.Index(page, heading)
		if at < 0 {
			continue // a group that holds only cobra's own help and completion has no page entries
		}

		if at < last {
			t.Errorf("group %q is out of order", g.Title)
		}

		last = at
	}

	for _, c := range cli.RootCommand().Commands() {
		if !c.IsAvailableCommand() {
			continue
		}

		link := "(" + strings.ReplaceAll(c.CommandPath(), " ", "_") + ".md)"
		if strings.Count(page, link) != 1 {
			t.Errorf("%s is listed %d times on the root page", c.CommandPath(), strings.Count(page, link))
		}
	}

	if !strings.Contains(page, "#### Get Started Commands\n") || strings.Contains(page, "ichor.md)") {
		t.Errorf("root page:\n%s", page)
	}
}
