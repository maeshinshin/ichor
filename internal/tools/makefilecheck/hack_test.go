package makefilecheck

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// `npm ci` follows a node_modules symlink and empties the directory it points at, so a script that links the real one into a
// throwaway copy and then runs a target that installs (make third-party-notices) wipes the checkout's dependencies.
func TestNoHackScriptSymlinksNodeModules(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join(repoRoot, "hack", "*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("glob hack/*.sh: %v (%d files)", err, len(scripts))
	}

	link := regexp.MustCompile(`ln\s+-\w*s\w*\s.*node_modules`)

	for _, p := range scripts {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}

		if m := link.Find(b); m != nil {
			t.Errorf("%s symlinks node_modules (%q): npm ci would empty the real directory", p, m)
		}
	}
}
