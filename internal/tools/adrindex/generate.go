package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// filenamePattern matches a real ADR file (a 4-digit-prefixed .md), the
// same pattern docs/adr/index_test.go used to check by hand before this
// command replaced it.
var filenamePattern = regexp.MustCompile(`^(\d{4})-.*\.md$`)

var (
	statusLine  = regexp.MustCompile(`(?m)^Status:\s*(.+?)\s*·\s*Date:`)
	decidedLine = regexp.MustCompile(`(?m)^Decided:\s*(.+)$`)
)

// entry is one ADR's index row.
type entry struct {
	Number  int
	File    string // file name in docs/adr, the target of the number's link
	Decided string
	Status  string
}

// readmePreamble is everything in docs/adr/README.md above the table; it
// is not generated from anything, so it is kept here rather than read
// back from the file this command overwrites.
const readmePreamble = `# Decision records

One file per decision, ` + "`NNNN-short-title.md`" + `, based on ` + "`0000-template.md`" + `.
Write one when you pick something the design doesn't pin down: a default, a
library, a metric name, a workaround. Keep it short. Don't rewrite accepted
records; supersede them.

The table below is generated from each file's ` + "`Decided:`/`Status:`" + ` header
line by ` + "`internal/tools/adrindex`" + ` (` + "`make adr-index`" + `) -- never hand-edit it.
Give a new ADR a ` + "`Decided:`" + ` line (` + "`0000-template.md`" + ` shows where) and
regenerate; CI's generated-artifact drift check catches a stale table the
same way it catches a stale chart.

`

// parseEntry extracts an entry from one ADR file's contents, failing
// loudly if either header line is missing: a silently-skipped ADR would
// be a worse failure mode than this command refusing to run.
func parseEntry(path string, number int, content string) (entry, error) {
	sm := statusLine.FindStringSubmatch(content)
	if sm == nil {
		return entry{}, fmt.Errorf("%s: no \"Status: ... · Date: ...\" line", path)
	}

	dm := decidedLine.FindStringSubmatch(content)
	if dm == nil {
		return entry{}, fmt.Errorf("%s: no \"Decided: ...\" line", path)
	}

	return entry{Number: number, File: filepath.Base(path), Decided: dm[1], Status: sm[1]}, nil
}

// parseADRs reads every real ADR file in dir (0000-template.md excepted,
// same as the test this replaces) and returns one entry per file, sorted
// by number.
func parseADRs(dir string) ([]entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var out []entry

	for _, e := range entries {
		if e.IsDir() || e.Name() == "0000-template.md" {
			continue
		}

		m := filenamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		number, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("%s: non-numeric ADR prefix: %w", e.Name(), err)
		}

		path := filepath.Join(dir, e.Name())

		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		parsed, err := parseEntry(path, number, string(content))
		if err != nil {
			return nil, err
		}

		out = append(out, parsed)
	}

	slices.SortFunc(out, func(a, b entry) int { return a.Number - b.Number })

	return out, nil
}

// renderReadme renders README.md's full content for entries, in sorted
// (so deterministic) order.
func renderReadme(entries []entry) string {
	var b strings.Builder

	b.WriteString(readmePreamble)
	b.WriteString("| # | Decided | Status |\n|---|---|---|\n")

	for _, e := range entries {
		fmt.Fprintf(&b, "| [%04d](%s) | %s | %s |\n", e.Number, e.File, e.Decided, e.Status)
	}

	return b.String()
}

// generateIndex regenerates dir/README.md from every ADR in dir, writing
// it only if the content actually changed.
func generateIndex(dir string) error {
	entries, err := parseADRs(dir)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, "README.md")

	want := renderReadme(entries)

	got, err := os.ReadFile(path)
	if err == nil && string(got) == want {
		return nil
	}

	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return err
	}

	fmt.Printf("regenerated %s from %d ADRs\n", path, len(entries))

	return nil
}
