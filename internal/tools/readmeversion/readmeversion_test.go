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

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// newestRelease returns the highest vX.Y.Z in the whitespace-separated tag list, or "".
func newestRelease(tags string) (newest string) {
	for tag := range strings.FieldsSeq(tags) {
		if releaseTag.MatchString(tag) && (newest == "" || semver.Compare(tag, newest) > 0) {
			newest = tag
		}
	}
	return newest
}

func TestNewestRelease(t *testing.T) {
	if got := newestRelease("v0.2.4 junk v0.10.0 v0.3.0-rc1 v0.9.9"); got != "v0.10.0" {
		t.Errorf("newestRelease = %q, want v0.10.0", got)
	}
	if got := newestRelease("v0.3.0-rc1"); got != "" {
		t.Errorf("newestRelease = %q, want none", got)
	}
}

// TestREADMEIsNotOlderThanNewestTag fails once a release is tagged and the README still names an older one.
// It allows a README that is ahead, so the bump can land before the tag.
//
// A release branch is compared with the tags it descends from, so a patch on release-0.1 is not held to v0.2.x;
// main, which descends from no tag, is compared with all of them. That needs the history, not only the tags.
// In CI (CI is set) tags that cannot be read, or a shallow checkout, fail the test; elsewhere it skips.
func TestREADMEIsNotOlderThanNewestTag(t *testing.T) {
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("CI") != "" {
			t.Fatalf(format+": the checkout needs fetch-depth: 0 (all tags and their history); fetch-tags: true alone is not enough", args...)
		}
		t.Skipf(format, args...)
	}
	if out, err := exec.Command("git", "rev-parse", "--is-shallow-repository").Output(); err != nil || strings.TrimSpace(string(out)) != "false" {
		unavailable("the git history is not complete (%v, shallow=%q)", err, strings.TrimSpace(string(out)))
	}
	newest := ""
	for _, args := range [][]string{{"--merged", "HEAD"}, {}} {
		out, err := exec.Command("git", append([]string{"tag", "-l", "v*"}, args...)...).Output()
		if err != nil {
			unavailable("git tag failed: %v", err)
		}
		if newest = newestRelease(string(out)); newest != "" {
			break
		}
	}
	if newest == "" {
		unavailable("no release tags")
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
