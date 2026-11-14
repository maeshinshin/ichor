package main

import (
	"bufio"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// ichorOwned lists the embedded paths that are Ichor's own or covered by NOTICE, so need no entry here.
var ichorOwned = map[string]bool{
	"cmd/ichor-ui/dist":                              true,
	"cmd/ichor-ui/placeholder":                       true,
	"internal/installmanifest/install.yaml":          true,
	"internal/installmanifest/network-policies.yaml": true,
	"internal/installmanifest/placeholder.yaml":      true,
}

// TestEmbeddedFilesAreInNotices fails when a binary embeds a file that THIRD-PARTY-NOTICES does not name,
// which is how a vendored upstream file slips out of the notices.
func TestEmbeddedFilesAreInNotices(t *testing.T) {
	notices, err := os.ReadFile("../../../THIRD-PARTY-NOTICES")
	if err != nil {
		t.Fatal(err)
	}

	list := exec.Command("git", "ls-files", "-z", "--", "*.go")
	list.Dir = "../../.."

	out, err := list.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	for src := range strings.SplitSeq(string(out), "\x00") {
		if src == "" {
			continue
		}

		b, err := os.ReadFile(filepath.Join("../../..", src))
		if err != nil {
			t.Fatal(err)
		}

		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			rest, ok := strings.CutPrefix(sc.Text(), "//go:embed ")
			if !ok {
				continue
			}

			for pat := range strings.FieldsSeq(rest) {
				// shortcut: literal embed patterns only, so a glob fails the check closed; expand globs here once one is embedded.
				p := path.Join(filepath.ToSlash(filepath.Dir(src)), pat)
				if ichorOwned[p] || strings.Contains(string(notices), p) {
					continue
				}

				t.Errorf("%s embeds %s, which THIRD-PARTY-NOTICES does not name and the Ichor-owned list does not cover", src, p)
			}
		}
	}
}
