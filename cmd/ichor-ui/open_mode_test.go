package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenModeIsRefusedAtStartup runs main with the removed flag, with the removed environment
// variable and with no sign-in configured at all. Each must stop the process with the instruction rather than start it read-only.
const wasRemoved = "was removed"

func TestOpenModeIsRefusedAtStartup(t *testing.T) {
	if args := os.Getenv("ICHOR_UI_TEST_MAIN_ARGS"); args != "" {
		os.Args = append([]string{"ichor-ui"}, strings.Fields(args)...)
		main()

		return
	}

	for name, tc := range map[string]struct {
		args string
		env  []string
		want string
	}{
		"flag":         {args: "--allow-unauthenticated=true", want: wasRemoved},
		"bare":         {args: "--allow-unauthenticated", want: wasRemoved},
		"variable":     {args: "--addr=:0", env: []string{"ICHOR_ALLOW_UNAUTHENTICATED=true"}, want: wasRemoved},
		"unconfigured": {args: "--addr=:0", want: "does not start without one"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOpenModeIsRefusedAtStartup$")
		// No cluster to find: a main that does start must not reach for whatever kubeconfig the test runs under.
		cmd.Env = append(os.Environ(), append([]string{
			"ICHOR_UI_TEST_MAIN_ARGS=" + tc.args, "KUBECONFIG=/nonexistent", "HOME=" + t.TempDir(), "KUBERNETES_SERVICE_HOST=",
		}, tc.env...)...)

		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: ichor-ui started with the open mode; output: %s", name, out)

			continue
		}

		if !strings.Contains(string(out), tc.want) || !strings.Contains(string(out), "sign-in") {
			t.Errorf("%s: ichor-ui stopped without saying what to do instead: %s", name, out)
		}
	}
}

// TestOnlyTheRefusalsNameTheRemovedFlag is the tripwire for a new way to start ichor-ui open: a file that
// builds a command line, a manifest or a chart template and names the removed flag, its variable or the chart
// value is either one of the places that refuse or strip it, or a new place that would pass it on. The
// allowed files are written out here and not found by searching for them, so a new one is a decision made in
// this list.
func TestOnlyTheRefusalsNameTheRemovedFlag(t *testing.T) {
	allowed := map[string]string{
		"cmd/ichor-ui/main.go":            "refuses to start with it",
		"internal/installer/customize.go": "drops it from a live Deployment on upgrade",
		"internal/cli/init_users.go":      "refuses it on init and adopt",
		"internal/cli/init_file.go":       "reads the init file's key to refuse it",
		"dist/chart/templates/ui/ui.yaml": "fails the render when ichorUI.allowUnauthenticated is true",
		"dist/chart/values.yaml":          "says the value is removed",
	}

	needles := []string{"allow-unauthenticated", "ALLOW_UNAUTHENTICATED", "allowUnauthenticated"}

	root := "../.."

	for _, dir := range []string{"cmd", "internal", "pkg", "config", "dist/chart/templates", "dist/chart/values.yaml", "hack", "test", ".github", "Makefile"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, path)

			for _, n := range needles {
				if _, ok := allowed[filepath.ToSlash(rel)]; !ok && strings.Contains(string(raw), n) {
					t.Errorf("%s names %q; nothing may pass the removed open mode on, and only the files that refuse or drop it may name it", rel, n)
				}
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
