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
