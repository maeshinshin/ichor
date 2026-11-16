package makefilecheck

import (
	"os/exec"
	"strings"
	"testing"
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
