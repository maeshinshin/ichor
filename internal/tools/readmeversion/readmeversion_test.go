// Package readmeversion keeps README.md's release number from going stale: the site fills the
// docs' version in from the build, but README.md is read on GitHub as written.
package readmeversion

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// TestREADMEIsNotOlderThanNewestTag fails once a release is tagged and the README still names an older one.
// It allows a README that is ahead, so the bump can land before the tag.
func TestREADMEIsNotOlderThanNewestTag(t *testing.T) {
	out, err := exec.Command("git", "tag", "-l", "v*").Output()
	if err != nil {
		t.Skipf("no git tags to compare with: %v", err)
	}
	newest := ""
	for tag := range strings.FieldsSeq(string(out)) {
		if re := regexp.MustCompile(`^v\d+\.\d+\.\d+$`); re.MatchString(tag) && (newest == "" || semver.Compare(tag, newest) > 0) {
			newest = tag
		}
	}
	if newest == "" {
		t.Skip("no release tags")
	}
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?:VERSION=|--version )(\d+\.\d+\.\d+)`).FindAllStringSubmatch(string(readme), -1) {
		if semver.Compare("v"+m[1], newest) < 0 {
			t.Errorf("README.md has %s, older than the newest tag %s: update the literals", m[1], newest)
		}
	}
}
