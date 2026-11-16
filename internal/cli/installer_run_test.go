/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKubeconfig points at a port nothing listens on. client.New does not dial, and the
// placeholder manifest makes installer.Run fail before it asks the cluster anything.
const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: t
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: t
  context: {cluster: t, user: t}
current-context: t
users:
- name: t
  user: {token: x}
`

// runInstallerRun drives installerRunCmd's RunE with termLog as the termination message file and
// returns what it returned and what it wrote to stderr.
func runInstallerRun(t *testing.T, termLog string) (string, error) {
	t.Helper()

	kc := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kc, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}

	prevKC, prevLog := kubeconfigPath, installerTerminationLog
	kubeconfigPath, installerTerminationLog = kc, termLog
	t.Cleanup(func() { kubeconfigPath, installerTerminationLog = prevKC, prevLog })

	var stderr bytes.Buffer
	installerRunCmd.SetErr(&stderr)
	installerRunCmd.SetContext(context.Background())
	t.Cleanup(func() { installerRunCmd.SetErr(nil) })

	err := installerRunCmd.RunE(installerRunCmd, nil)

	return stderr.String(), err
}

// TestInstallerRun_WritesItsErrorToTheTerminationLog: the installer's error is the file's content,
// so the controller can show it on the paused operation.
func TestInstallerRun_WritesItsErrorToTheTerminationLog(t *testing.T) {
	termLog := filepath.Join(t.TempDir(), "termination-log")
	if err := os.WriteFile(termLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, runErr := runInstallerRun(t, termLog)
	if runErr == nil {
		t.Fatal("installer run returned nil, want its error")
	}

	got, err := os.ReadFile(termLog)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != runErr.Error() {
		t.Errorf("termination message = %q, want the returned error %q", got, runErr.Error())
	}
}

// TestInstallerRun_AFailedWriteDoesNotSwallowTheError: a termination log that cannot be opened
// is reported on stderr, and the installer's error still comes back unchanged.
func TestInstallerRun_AFailedWriteDoesNotSwallowTheError(t *testing.T) {
	good := filepath.Join(t.TempDir(), "termination-log")
	if err := os.WriteFile(good, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, want := runInstallerRun(t, good)

	// Not created: the write must not create it, so its open fails.
	missing := filepath.Join(t.TempDir(), "no-such-file")

	stderr, got := runInstallerRun(t, missing)
	if got == nil {
		t.Fatal("installer run returned nil, want its error")
	}

	if want == nil || got.Error() != want.Error() {
		t.Errorf("error with a failed write = %v, want the unchanged %v", got, want)
	}

	if !strings.Contains(stderr, "could not write the termination message") {
		t.Errorf("stderr = %q, want the write failure reported", stderr)
	}
}

func TestTruncateAtRune(t *testing.T) {
	// 4094 ASCII bytes then "é" (two bytes, at 4094 and 4095): a cut at 4095 splits it, so it backs off.
	multi := strings.Repeat("a", 4094) + "é"

	tests := []struct {
		name, in string
		limit    int
		want     string
	}{
		{"short string is unchanged", "installer failed", 4095, "installer failed"},
		{"ascii cut at the limit", strings.Repeat("a", 10), 4, "aaaa"},
		{"cut backs off a split rune", multi, 4095, strings.Repeat("a", 4094)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateAtRune(tt.in, tt.limit); got != tt.want {
				t.Errorf("truncateAtRune = %q, want %q", got, tt.want)
			}
		})
	}
}
