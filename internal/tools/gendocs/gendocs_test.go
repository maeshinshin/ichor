package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

// TestDeprecatedMarksOnlyDeprecatedFields: a description that merely mentions "deprecated" does not mark
// its field, and a field that is deprecated still is.
func TestDeprecatedMarksOnlyDeprecatedFields(t *testing.T) {
	out := t.TempDir()
	if err := generate(filepath.Join(repoRoot, crdDir), out); err != nil {
		t.Fatalf("generate: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(out, "crds", "taloscluster.md"))
	if err != nil {
		t.Fatal(err)
	}

	rows := string(b)
	for _, live := range []string{"`spec.cni.preset`", "`spec.endpoint.type`"} {
		if strings.Contains(rows, live+" **(deprecated)**") {
			t.Errorf("%s is marked (deprecated)", live)
		}
	}

	// positive control: spec.documents of TalosConfigPatch says it is deprecated itself
	b, err = os.ReadFile(filepath.Join(out, "crds", "talosconfigpatch.md"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(b), "`spec.documents` **(deprecated)**") {
		t.Error("spec.documents is no longer marked (deprecated)")
	}
}

var (
	flagMention = regexp.MustCompile(`(?:^|[^\w-])--([a-z][a-z0-9-]*)`)
	flagDefined = regexp.MustCompile(`(?m)^\s+(?:-\w, )?--([a-z][a-z0-9-]*)`)
)

// A page that names a --flag in its synopsis or in another flag's usage must list that flag under
// Options or inherited Options; a hidden flag is listed nowhere, so naming it sends the reader to
// something the page cannot explain.
func TestNamedFlagsAreListed(t *testing.T) {
	dir := t.TempDir()
	if err := generateCLI(dir); err != nil {
		t.Fatal(err)
	}

	pages, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no generated pages: %v", err)
	}

	// A group page names its subcommands' flags, and a few pages name another tool's or command's
	// flag in quotes; both are skipped, the second by page and flag.
	groups := map[string]bool{}

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		if c.HasSubCommands() {
			groups[strings.ReplaceAll(c.CommandPath(), " ", "_")+".md"] = true
		}

		for _, sc := range c.Commands() {
			walk(sc)
		}
	}
	walk(cli.RootCommand())

	foreign := map[string]bool{
		"ichor_restore_cluster.md/recover-from": true, // quotes 'talosctl bootstrap --recover-from'
		"ichor_rotate_ca.md/acknowledge":        true,
		"ichor_rotate_ca.md/bundle":             true, // quotes the command line of `ichor create credentials`
		"ichor_rotate_ca.md/kube-server":        true,
		"ichor_rotate_ca.md/talos-endpoints":    true,
	}

	for _, p := range pages {
		if groups[filepath.Base(p)] {
			continue
		}

		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}

		page := string(b)
		synopsis, options := "", ""

		if i := strings.Index(page, "### Synopsis"); i >= 0 {
			synopsis = page[i:]
			if j := strings.Index(synopsis, "\n### Examples"); j >= 0 {
				synopsis = synopsis[:j]
			} else if j := strings.Index(synopsis, "\n### Options"); j >= 0 {
				synopsis = synopsis[:j]
			}
		}

		if i := strings.Index(page, "### Options"); i >= 0 {
			options = page[i:]
			if j := strings.Index(options, "### SEE ALSO"); j >= 0 {
				options = options[:j]
			}
		}

		listed := map[string]bool{}
		for _, m := range flagDefined.FindAllStringSubmatch(options, -1) {
			listed[m[1]] = true
		}

		for _, text := range []string{synopsis, options} {
			for _, m := range flagMention.FindAllStringSubmatch(text, -1) {
				if !listed[m[1]] && !foreign[filepath.Base(p)+"/"+m[1]] {
					t.Errorf("%s names --%s, which its Options do not list", filepath.Base(p), m[1])
				}
			}
		}
	}
}
