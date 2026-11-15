package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestOpenModeIsRefusedAtStartup runs main with the removed flag and with the removed environment
// variable. Both must stop the process with the instruction rather than start it read-only.
func TestOpenModeIsRefusedAtStartup(t *testing.T) {
	if args := os.Getenv("ICHOR_UI_TEST_MAIN_ARGS"); args != "" {
		os.Args = append([]string{"ichor-ui"}, strings.Fields(args)...)
		main()

		return
	}

	for name, tc := range map[string]struct {
		args string
		env  []string
	}{
		"flag":     {args: "--allow-unauthenticated=true"},
		"bare":     {args: "--allow-unauthenticated"},
		"variable": {args: "--addr=:0", env: []string{"ICHOR_ALLOW_UNAUTHENTICATED=true"}},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOpenModeIsRefusedAtStartup$")
		cmd.Env = append(os.Environ(), append([]string{"ICHOR_UI_TEST_MAIN_ARGS=" + tc.args}, tc.env...)...)

		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: ichor-ui started with the open mode; output: %s", name, out)

			continue
		}

		if !strings.Contains(string(out), "was removed") || !strings.Contains(string(out), "turn sign-in on") {
			t.Errorf("%s: ichor-ui stopped without saying what to do instead: %s", name, out)
		}
	}
}
