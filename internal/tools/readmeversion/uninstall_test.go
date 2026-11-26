package readmeversion

import (
	"os"
	"strings"
	"testing"
)

// TestUninstallAnnotatesEveryProtectedSecret holds both uninstall procedures to the label selector: the bundle
// Secrets and the Cilium prior-release Secret all carry ichor.maesh.dev/protected, and a name list misses one.
func TestUninstallAnnotatesEveryProtectedSecret(t *testing.T) {
	const want = "annotate secret -l ichor.maesh.dev/protected ichor.maesh.dev/allow-delete=true"
	for _, f := range []string{"../../../docs/guides/install-with-helm/upgrade.md", "../../../docs/guides/troubleshooting.md"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s: the uninstall steps do not run `kubectl ... %s`", f, want)
		}
	}
}
