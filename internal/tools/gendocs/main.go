// Command gendocs writes the generated part of the documentation under docs/reference: one page
// per ichor command (from the real cobra command tree, docs/reference/cli) and one page per
// custom resource (from the CRD schemas in config/crd/bases, docs/reference/crds). Every page
// starts with the YAML frontmatter the docs site reads. It also writes the field index the web UI
// searches (ui/src/gen/fieldindex.json): the settings of spec.config, one row each, from the TalosConfigPatch
// schema, with the descriptions and allowed values from the Talos machinery documentation tables.
// The index is not committed, because the repository holds none of Talos's prose; "make ui-fieldindex"
// writes it, and every UI target that reads it depends on that.
//
// Run via "make docs-reference". The test in this package fails when the committed pages are
// stale, so a change to a command, a flag or an API type that is not followed by a regeneration
// is caught.
package main

import (
	"fmt"
	"os"
)

const (
	crdDir    = "config/crd/bases"
	outputDir = "docs/reference"
	// indexFile is the field search index the UI bundles (see fieldindex.go).
	indexFile = "ui/src/gen/fieldindex.json"
)

func main() {
	// "gendocs fieldindex" writes only the index: the UI's build needs it, not the pages.
	if len(os.Args) > 1 && os.Args[1] == "fieldindex" {
		if err := generateFieldIndex(crdDir, indexFile); err != nil {
			fmt.Fprintln(os.Stderr, "gendocs:", err)
			os.Exit(1)
		}

		return
	}

	if err := generate(crdDir, outputDir); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

// generate writes both references below out, replacing what a previous run left there.
func generate(crds, out string) error {
	if err := generateCLI(out + "/cli"); err != nil {
		return fmt.Errorf("cli reference: %w", err)
	}

	if err := generateCRDs(crds, out+"/crds"); err != nil {
		return fmt.Errorf("crd reference: %w", err)
	}

	return nil
}
