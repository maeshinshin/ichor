// Package docsciliumversion keeps the guides and concepts from naming a Cilium release other than the chart's.
package docsciliumversion

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

var ciliumVersion = regexp.MustCompile(`Cilium v?(\d+\.\d+)(?:\.\d+)?`)

// floors are phrasings that name a minimum Cilium, not the shipped one: the whole match, and why it may differ.
// None is needed today.
var floors = map[string]string{}

func TestCiliumVersionsMatchTheChart(t *testing.T) {
	want := strings.Join(strings.SplitN(cilium.ChartVersion, ".", 3)[:2], ".")
	for _, dir := range []string{"../../../docs/guides", "../../../docs/concepts"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range ciliumVersion.FindAllStringSubmatchIndex(string(b), -1) {
				lit, minor := string(b[m[0]:m[1]]), string(b[m[2]:m[3]])
				if _, ok := floors[lit]; !ok && minor != want {
					t.Errorf("%s: %q, the chart is Cilium %s", path, lit, cilium.ChartVersion)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// moved lists the files hack/bump-cilium-patch.sh (or a Cilium minor bump) rewrites or that record a dated
// check, so a version literal in their comments is fine.
var moved = map[string]bool{
	"pkg/cni/cilium/values.go":                                   true,
	"pkg/cni/cilium/render.go":                                   true,
	"pkg/release/compat.go":                                      true,
	"test/internal/foreign/foreign.go":                           true,
	"test/e2e/foreign/cilium_test.go":                            true,
	"internal/tools/docsciliumversion/docsciliumversion_test.go": true,
}

// TestCommentsNameNoChartVersion fails when a Go or Makefile comment spells out a patch of the embedded minor:
// the bump script does not move those, so they go stale. Point to cilium.ChartVersion instead.
func TestCommentsNameNoChartVersion(t *testing.T) {
	minor := strings.Join(strings.SplitN(cilium.ChartVersion, ".", 3)[:2], ".")
	lit := regexp.MustCompile(`(^|[^0-9.v])` + regexp.QuoteMeta(minor) + `\.\d+`)
	root := "../../.."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "bin", "ui", "site", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(rel)
		if (base != "Makefile" && !strings.HasSuffix(base, ".go")) || moved[rel] ||
			strings.HasPrefix(base, "zz_generated") || strings.HasSuffix(base, ".pb.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if s := strings.TrimSpace(line); (strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#")) && lit.MatchString(s) {
				t.Errorf("%s:%d names a Cilium %s patch in a comment; say cilium.ChartVersion instead: %s", rel, i+1, minor, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
