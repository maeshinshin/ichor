package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetFlags puts every flag of cmd back to its default, so a flag a test set does not reach the next one.
func resetFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()

	t.Cleanup(func() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		})
	})
}

// TestInitAndAdoptRefuseTheRemovedOpenMode runs the real commands with --allow-unauthenticated, in the
// spellings a script has: bare, true, and (a flag that was removed is removed, whatever it was set to)
// false. Each is refused before anything else, with what to do instead, and not with cobra's "unknown flag".
func TestInitAndAdoptRefuseTheRemovedOpenMode(t *testing.T) {
	// A command that were not to refuse would go on to read the cluster: it must find none.
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	t.Setenv("HOME", t.TempDir())

	resetFlags(t, initCmd)
	resetFlags(t, adoptCmd)

	for name, args := range map[string][]string{
		"init bare":         {"init", "--allow-unauthenticated"},
		"init true":         {"init", "--allow-unauthenticated=true"},
		"init false":        {"init", "--allow-unauthenticated=false"},
		"adopt bare":        {"adopt", "--cluster", "c", "--allow-unauthenticated"},
		"adopt true":        {"adopt", "--cluster", "c", "--allow-unauthenticated=true"},
		"adopt false":       {"adopt", "--cluster", "c", "--allow-unauthenticated=false"},
		"adopt add-machine": {"adopt", "--cluster", "c", "--add-machine", "10.0.0.5", "--allow-unauthenticated"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := run(t, args...)
			if err == nil {
				t.Fatalf("%v succeeded:\n%s", args, out)
			}

			for _, want := range []string{"--allow-unauthenticated was removed", "sign-in is on by default", "--ui-url"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}

			if strings.Contains(err.Error(), "unknown flag") {
				t.Errorf("the flag is unknown, not refused with the instruction: %v", err)
			}
		})
	}
}

// TestInitFileThatSetsTheRemovedOpenModeIsRefused: the init file's ui.allowUnauthenticated is the same
// switch, so a file that still sets it gets the same refusal.
func TestInitFileThatSetsTheRemovedOpenModeIsRefused(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	t.Setenv("HOME", t.TempDir())

	resetFlags(t, initCmd)

	path := filepath.Join(t.TempDir(), "init.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(initConfigTemplate, "  adminUser: admin ", "  allowUnauthenticated: true\n  adminUser: admin ", 1)), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { initFile = "" })

	_, err := run(t, "init", "-f", path)
	if err == nil || !strings.Contains(err.Error(), "--allow-unauthenticated was removed") {
		t.Fatalf("init -f = %v, want the refusal", err)
	}
}

// TestTheInitFileTemplateOffersNoOpenMode: the example a person copies must not teach the removed switch.
func TestTheInitFileTemplateOffersNoOpenMode(t *testing.T) {
	if strings.Contains(strings.ToLower(initConfigTemplate), "unauthenticated") {
		t.Error("the init file template still names the open mode")
	}

	for _, c := range []*cobra.Command{initCmd, adoptCmd} {
		if f := c.Flags().Lookup(flagAllowUnauthenticated); f == nil || !f.Hidden {
			t.Errorf("%s: the removed flag must stay registered, to be refused, and hidden from help", c.Name())
		}
	}
}
