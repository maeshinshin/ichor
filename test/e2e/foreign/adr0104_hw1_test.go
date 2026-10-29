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

// ADR 0104 cases E5, E6, F1, F4, F5. They run against a cluster booted by `go run ./test/harness/foreign up`
// (ICHOR_E2E_F_ATTACH=1), so one cluster serves the cases that do not change its CNI state.

import (
	"os"
	"os/exec"
	"strings"
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

// releaseState lists the Helm release records (status label) of the foreign cluster, in every namespace.
func releaseState(c *foreign.Cluster) string {
	out, _ := inner(c, "get", "secrets", "-A", "-l", "owner=helm", "-o",
		"jsonpath={range .items[*]}{.metadata.namespace}/{.metadata.name} status={.metadata.labels.status}{\"\\n\"}{end}")
	return strings.TrimSpace(out)
}

// TestF5HelmTemplateNoCNI (ADR 0104 F5): helm template and helm install --dry-run on a no-CNI cluster do not
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
