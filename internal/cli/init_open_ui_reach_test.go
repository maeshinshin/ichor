package cli

import (
	"os"
	"strings"
	"testing"
)

// TestOpenUIReachIsTheSameInTheDocs: the sentence that says what an ichor-ui without sign-in can do is
// defined once, in openUIReach, and the init flag and the init warning use it. The reference and the two guides
// carry it as prose, so a change to the constant that the docs do not follow fails here.
func TestOpenUIReachIsTheSameInTheDocs(t *testing.T) {
	for _, path := range []string{
		"../../docs/reference/cli/ichor_init.md",
		"../../docs/guides/getting-started/open-the-ui-and-sign-in.md",
		"../../docs/guides/getting-started/create-the-cluster/settings-every-pattern.md",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(strings.Join(strings.Fields(string(raw)), " "), openUIReach) {
			t.Errorf("%s does not carry the sentence %q", path, openUIReach)
		}
	}
}
