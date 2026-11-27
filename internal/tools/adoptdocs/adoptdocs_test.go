// Package adoptdocs keeps the docs from describing adopt's install step as it was before the version
// guard: adopt applies Ichor's install manifest to a missing or partial install, refuses a running Ichor
// of another version, and refuses a leftover IchorInstallation with no controller Deployment.
package adoptdocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const docsRoot = "../../../docs"

var stale = []string{
	"if Ichor is not running",
	"adopt takes Ichor for installed and installs nothing",
	"It installs Ichor when it is absent, then",
}

func TestDocsDoNotDescribeAdoptsInstallStepAsItWas(t *testing.T) {
	t.Parallel()

	seen := 0

	err := filepath.WalkDir(docsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") || strings.Contains(p, "/adr/") {
			return err
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		seen++

		for _, s := range stale {
			if strings.Contains(string(b), s) {
				t.Errorf("%s says %q, which no longer describes adopt's install step", p, s)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if seen == 0 {
		t.Fatal("no docs read from " + docsRoot)
	}
}
