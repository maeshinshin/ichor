package makefilecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// repoRoot is the module root, relative to this package's directory, where `go test` runs.
const repoRoot = "../../.."

// makeOut runs make with the Makefile at the module root and returns its combined output.
func makeOut(t *testing.T, args ...string) (string, error) {
	t.Helper()

	out, err := exec.Command("make", append([]string{"-s", "-C", repoRoot}, args...)...).CombinedOutput()

	return string(out), err
}

// TestEmbedImagesRefusesAnImageWithoutAVersionTag names the image that failed: a tag of latest, or no tag at all, is
// not a version. A registry port (localhost:5000/...) is not a tag.
func TestEmbedImagesRefusesAnImageWithoutAVersionTag(t *testing.T) {
	const ctl, ui = "ghcr.io/maeshinshin/ichor-controller", "ghcr.io/maeshinshin/ichor-ui"

	refused := []struct{ name, img, imgUI, want string }{
		{"controller tagged latest", ctl + ":latest", ui + ":v0.1.0", "IMG is"},
		{"controller without a tag", ctl, ui + ":v0.1.0", "IMG is"},
		{"ui tagged latest", ctl + ":v0.1.0", ui + ":latest", "IMG_UI is"},
		{"ui without a tag", ctl + ":v0.1.0", ui, "IMG_UI is"},
		{"registry port and no tag", "localhost:5000/ichor-controller", ui + ":v0.1.0", "IMG is"},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			out, err := makeOut(t, "check-embed-images", "IMG="+tc.img, "IMG_UI="+tc.imgUI)
			if err == nil {
				t.Fatalf("check-embed-images accepted IMG=%s IMG_UI=%s", tc.img, tc.imgUI)
			}

			if !strings.Contains(out, tc.want) {
				t.Errorf("the refusal does not name the image that failed (want %q):\n%s", tc.want, out)
			}
		})
	}

	for _, pair := range [][2]string{{ctl + ":v0.1.0", ui + ":v0.1.0"}, {"localhost:5000/ichor-controller:v0.1.0", ui + ":v0.1.0"}} {
		if out, err := makeOut(t, "check-embed-images", "IMG="+pair[0], "IMG_UI="+pair[1]); err != nil {
			t.Errorf("check-embed-images refused a tagged pair %v: %v\n%s", pair, err, out)
		}
	}
}

// TestEmbedBuildsTheInstallerOnlyAfterTheCheck pins build-ichor-embed's prerequisites to the check alone. Under make -k a
// prerequisite still runs after a failed check, and build-installer would then rewrite dist/install.yaml with the
// placeholders; the recipe runs build-installer only once the check has passed.
func TestEmbedBuildsTheInstallerOnlyAfterTheCheck(t *testing.T) {
	db, err := exec.Command("make", "-s", "-C", repoRoot, "-p", "-n", "build-ichor-embed").Output()
	if err != nil {
		t.Fatal(err)
	}

	const rule = "build-ichor-embed:"

	seen := 0

	for line := range strings.Lines(string(db)) {
		if !strings.HasPrefix(line, rule) {
			continue
		}

		seen++

		if got := strings.Fields(strings.TrimPrefix(line, rule)); !slices.Equal(got, []string{"check-embed-images"}) {
			t.Errorf("build-ichor-embed has prerequisites %q, want only check-embed-images", got)
		}
	}

	if seen == 0 {
		t.Fatal("make -p printed no build-ichor-embed rule")
	}
}

// releaseVersionPattern returns the grep -E pattern that the release target checks VERSION with. It is read from the
// Makefile, so the test pins what make runs.
func releaseVersionPattern(t *testing.T) string {
	t.Helper()

	src, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}

	inRelease := false

	for line := range strings.Lines(string(src)) {
		if strings.HasPrefix(line, "release:") {
			inRelease = true
			continue
		}

		if _, rest, ok := strings.Cut(line, "grep -Eq '"); inRelease && ok {
			pattern, _, _ := strings.Cut(rest, "' ||")

			return strings.ReplaceAll(pattern, "$$", "$")
		}
	}

	t.Fatal("the release target has no grep -Eq check of VERSION")

	return ""
}

// TestReleaseVersionIsSemver requires the release target's VERSION check to agree with semver.IsValid. A numeric
// prerelease identifier with a leading zero (v0.1.0-0123456) is not semver, so make release must refuse it.
func TestReleaseVersionIsSemver(t *testing.T) {
	pattern := releaseVersionPattern(t)

	for _, v := range []string{
		"v0.1.0", "v10.20.30", "v0.1.0-0", "v0.1.0-rc.1", "v0.1.0-alpha-01", "v0.1.0-0a", "v0.1.0-1.0a",
		"v0.0.8-g0123456", "v0.0.8-g9794517",
		"v0.1.0-0123456", "v0.1.0-01", "v0.1.0-rc.01", "v0.1.0-1.00", "v0.1.0-", "v0.1.0-a..b", "v01.0.0",
	} {
		t.Run(v, func(t *testing.T) {
			c := exec.Command("grep", "-Eq", pattern)
			c.Stdin = strings.NewReader(v + "\n")

			matched := c.Run() == nil
			if want := semver.IsValid(v); matched != want {
				t.Errorf("the release pattern accepts %q = %v, but semver.IsValid says %v", v, matched, want)
			}
		})
	}
}
