//go:build e2e
// +build e2e

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

package foreign

// Cases E5, E6, F1, F4, F5. They run against a cluster booted by `go run ./test/harness/foreign up`
// (ICHOR_E2E_F_ATTACH=1), so one cluster serves the cases that do not change its CNI state.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/utils"
)

// altIchor is a second CLI build (another version, optionally with an unpullable image tag): ICHOR_E2E_F_ICHOR_ALT_BIN.
func altIchor(t *testing.T) string {
	t.Helper()
	p := os.Getenv("ICHOR_E2E_F_ICHOR_ALT_BIN")
	if p == "" {
		t.Skip("ICHOR_E2E_F_ICHOR_ALT_BIN must be set")
	}
	return repoPath(t, p)
}

// stageAlt copies the alternate CLI into the tools pod as /tmp/ichor-alt.
func stageAlt(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	pod := stage(t, c)
	if _, err := utils.Run(kube.Command("-n", ns, "cp", altIchor(t), pod+":/tmp/ichor-alt")); err != nil {
		t.Fatalf("copying the alternate ichor: %v", err)
	}
	if _, err := utils.Run(kube.Command("-n", ns, "exec", pod, "--", "chmod", "+x", "/tmp/ichor-alt")); err != nil {
		t.Fatal(err)
	}
}

// adoptKilled runs `ichor adopt` in the tools pod and kills it (SIGKILL, in the pod) killAfter after its output
// first contains trigger. It returns the output and whether the kill happened.
func adoptKilled(t *testing.T, c *foreign.Cluster, trigger string, killAfter time.Duration, extra ...string) (string, bool) {
	t.Helper()
	pod := stage(t, c)
	kill := func() {
		kc := exec.Command("kubectl", "--context", os.Getenv(kube.ContextEnv), "-n", ns, "exec", pod, "--", "pkill", "-9", "ichor") //nolint:gosec // fixed
		o, kerr := kc.CombinedOutput()
		t.Logf("pkill -9 ichor at %s: %v %s", time.Now().Format("15:04:05"), kerr, o)
	}
	out, _, ran, _ := adoptTriggered(t, c, "/tmp/ichor", trigger, killAfter, kill, extra...)
	return out, ran
}

// adoptTriggered runs `ichor adopt` (the binary at bin in the tools pod) and runs action delay after its output first
// contains trigger. It returns the output, how long adopt ran, whether the action ran, and adopt's error.
func adoptTriggered(t *testing.T, c *foreign.Cluster, bin, trigger string, delay time.Duration, action func(), extra ...string) (string, time.Duration, bool, error) {
	t.Helper()
	pod := stage(t, c)
	args := append([]string{"-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", bin, "adopt",
		"--cluster", c.Opts.Prefix, "-n", crNS, "--talosconfig", "/tmp/talosconfig", "--kubeconfig", "/tmp/kubeconfig",
		"--node", c.CPs[0], "--yes"}, append(recoverArgs(c, extra), extra...)...)
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--context", os.Getenv(kube.ContextEnv)}, args...)...)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var out strings.Builder
	var ran bool
	start := time.Now()
	sc := bufio.NewScanner(pipe)
	for sc.Scan() {
		line := sc.Text()
		mu.Lock()
		out.WriteString(line + "\n")
		mu.Unlock()
		t.Logf("%s [%s]", line, time.Now().Format("15:04:05"))
		if !ran && strings.Contains(line, trigger) {
			ran = true
			go func() {
				time.Sleep(delay)
				action()
			}()
		}
	}
	err = cmd.Wait()
	t.Logf("adopt exited: %v", err)
	mu.Lock()
	defer mu.Unlock()
	return out.String(), time.Since(start), ran, err
}

// releaseState lists the Helm release records (status label) of the foreign cluster, in every namespace.
func releaseState(c *foreign.Cluster) string {
	out, _ := inner(c, "get", "secrets", "-A", "-l", "owner=helm", "-o",
		"jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name} status={.metadata.labels.status}{\"\\n\"}{end}")
	return strings.TrimSpace(out)
}

// TestF5HelmTemplateNoCNI: helm template and helm install --dry-run on a no-CNI cluster do not
// trip the preflight. Then F4's second half: --require-ichor with no Ichor is refused and nothing is written.
func TestF5HelmTemplateNoCNI(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	before := cniPaths(t, c)
	chart := packagedChart(t)
	if out, err := local(t, 2*time.Minute, "helm", "template", "ichor", chart, "-n", crNS); err != nil || strings.Contains(out, "this cluster has no CNI") {
		t.Errorf("helm template failed or tripped the preflight: %v", err)
	}
	if out, err := local(t, 2*time.Minute, "helm", "install", "ichor", chart, "--dry-run", "--kubeconfig", c.LocalKube, "-n", crNS, "--create-namespace"); err != nil || strings.Contains(out, "this cluster has no CNI") {
		t.Errorf("helm install --dry-run failed or tripped the preflight: %v", err)
	}
	// ponytail: a server-side dry run does lookups; its outcome is recorded, not asserted.
	_, _ = local(t, 2*time.Minute, "helm", "install", "ichor", chart, "--dry-run=server", "--kubeconfig", c.LocalKube, "-n", crNS, "--create-namespace")
	noCNINothingWritten(t, c, before)

	out, err := adopt(t, c, "--require-ichor")
	if err == nil || !strings.Contains(out, "require-ichor") {
		t.Errorf("adopt --require-ichor with no Ichor was not refused (err=%v)", err)
	}
	noCNINothingWritten(t, c, before)
}
