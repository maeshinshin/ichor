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

// The refusal messages check-embed-images prints, one per image variable.
const (
	imgRefusal   = "IMG is"
	imgUIRefusal = "IMG_UI is"
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
		{"controller tagged latest", ctl + ":latest", ui + ":v0.1.0", imgRefusal},
		{"controller without a tag", ctl, ui + ":v0.1.0", imgRefusal},
		{"ui tagged latest", ctl + ":v0.1.0", ui + ":latest", imgUIRefusal},
		{"ui without a tag", ctl + ":v0.1.0", ui, imgUIRefusal},
		{"registry port and no tag", "localhost:5000/ichor-controller", ui + ":v0.1.0", imgRefusal},
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

// fakeGo stands in for go on PATH in the go-install-tool test. Asked for GOVERSION while FAKE_GOVERSION is set, it
// reports that version, as a toolchain other than the one that built the tool would. It records each go install and
// writes a stand-in tool; every other subcommand runs the real go.
const fakeGo = `#!/bin/sh
if [ "$1" = env ] && [ "$2" = GOVERSION ] && [ -n "$FAKE_GOVERSION" ]; then echo "$FAKE_GOVERSION"; exit 0; fi
if [ "$1" = install ]; then
	echo "$2" >> "$MARKER"
	name=$(basename "${2%@*}")
	printf '#!/bin/sh\n' > "$GOBIN/$name" && chmod +x "$GOBIN/$name"
	exit 0
fi
exec "$REAL_GO" "$@"
`

// TestGoInstallToolRebuildsAToolFromAnotherToolchain pins go-install-tool to the Go that built a versioned tool. A tool
// built by the running Go is kept; one built by another Go is rebuilt, because go version reports the toolchain a Go
// binary was built with.
func TestGoInstallToolRebuildsAToolFromAnotherToolchain(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, goVersion string // goVersion is what the current go reports; "" keeps the real one
		rebuilt         bool
	}{
		{"built by the current go", "", false},
		{"built by another go", "go1.0.0", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin, fake := filepath.Join(dir, "bin"), filepath.Join(dir, "fake")
			for _, d := range []string{bin, fake} {
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(filepath.Join(fake, "go"), []byte(fakeGo), 0o755); err != nil {
				t.Fatal(err)
			}

			// The versioned tool is the real go binary, so go version reads the toolchain it was built with.
			tool := filepath.Join(bin, "controller-gen")
			versioned := tool + "-v0.0.1"
			if err := os.Symlink(realGo, versioned); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(versioned, tool); err != nil {
				t.Fatal(err)
			}

			marker := filepath.Join(dir, "installed")
			cmd := exec.Command("make", "-s", "-C", repoRoot, "controller-gen", "LOCALBIN="+bin, "CONTROLLER_TOOLS_VERSION=v0.0.1")
			cmd.Env = append(os.Environ(), "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"),
				"REAL_GO="+realGo, "MARKER="+marker)
			if tc.goVersion != "" {
				cmd.Env = append(cmd.Env, "FAKE_GOVERSION="+tc.goVersion)
			}

			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make controller-gen: %v\n%s", err, out)
			}

			_, statErr := os.Stat(marker)
			if rebuilt := statErr == nil; rebuilt != tc.rebuilt {
				t.Errorf("go-install-tool rebuilt the tool = %v, want %v; make output:\n%s", rebuilt, tc.rebuilt, out)
			}
		})
	}
}

// TestEmbedWarnsOnARegistryPort pins the warning for an IMG that names a registry host with a port. The nodes pull it
// over HTTPS unless a registry mirror covers that host, and a plain-HTTP registry then fails with ImagePullBackOff. The
// build cannot see the mirror, so it warns and still succeeds.
func TestEmbedWarnsOnARegistryPort(t *testing.T) {
	const ctl, ui = "ghcr.io/maeshinshin/ichor-controller:v0.1.0", "ghcr.io/maeshinshin/ichor-ui:v0.1.0"

	warned := []struct{ img, imgUI, named string }{
		{"192.168.0.8:5000/maeshinshin/ichor-controller:v0.1.0", ui, "IMG is 192.168.0.8:5000/"},
		{ctl, "192.168.0.8:5000/maeshinshin/ichor-ui:v0.1.0", "IMG_UI is 192.168.0.8:5000/"},
	}

	for _, tc := range warned {
		out, err := makeOut(t, "check-embed-images", "IMG="+tc.img, "IMG_UI="+tc.imgUI)
		if err != nil {
			t.Fatalf("check-embed-images refused IMG=%s IMG_UI=%s, which it should only warn about: %v\n%s", tc.img, tc.imgUI, err, out)
		}

		if !strings.Contains(out, "warning") || !strings.Contains(out, tc.named) {
			t.Errorf("no warning names %q for IMG=%s IMG_UI=%s:\n%s", tc.named, tc.img, tc.imgUI, out)
		}
	}

	// Mirrored names and a bare name with no registry host are not warned about.
	for _, pair := range [][2]string{{ctl, ui}, {"controller:v0.1.0", ui}} {
		out, err := makeOut(t, "check-embed-images", "IMG="+pair[0], "IMG_UI="+pair[1])
		if err != nil || strings.Contains(out, "warning") {
			t.Errorf("check-embed-images warned or failed for IMG=%s IMG_UI=%s (err %v):\n%s", pair[0], pair[1], err, out)
		}
	}
}
