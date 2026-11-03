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

// D4 and D6 on a D1 cluster: `foreign up` (flannel, the default Kubernetes), then TestD1FlannelAdoptedUnmanaged,
// then these, each against the kept cluster (ICHOR_E2E_F_ATTACH=1, ICHOR_E2E_F_KEEP=1).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

// opsList prints every TalosOperation as name kind=phase.
func opsList(c *foreign.Cluster, kc string) string {
	out, _ := utils.Run(kube.InnerCommand(kc, "-n", crNS, "get", "talosoperations.ichor.maesh.dev", "-o",
		"jsonpath={range .items[*]}{.metadata.name} {.spec.kind}={.status.phase}{\"\\n\"}{end}"))
	return strings.TrimSpace(out)
}

// waitOps polls until every TalosOperation of the given kind is Completed (and at least one exists); a Failed one fails the test.
func waitOps(t *testing.T, c *foreign.Cluster, kc, kind string, limit time.Duration) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < limit {
		var mine, done int
		list := opsList(c, kc)
		for _, l := range strings.Split(list, "\n") {
			if strings.Contains(strings.ToLower(l), strings.ToLower(kind)+"=") {
				mine++
				if strings.HasSuffix(l, "=Completed") {
					done++
				}
				if strings.HasSuffix(l, "=Failed") {
					t.Fatalf("operation failed after %s:\n%s", time.Since(start).Round(time.Second), list)
				}
			}
		}
		t.Logf("%s operations (%s): %d of %d Completed\n%s", time.Since(start).Round(time.Second), kind, done, mine, list)
		if mine > 0 && done == mine {
			return
		}
		time.Sleep(20 * time.Second)
	}
	t.Fatalf("%s operation did not complete in %s", kind, limit)
}

// noHandover asserts the cluster never grew a CNIHandover condition or an ichor-cni Job (D4: an upgrade is not blocked by one).
func noHandover(t *testing.T, c *foreign.Cluster, kc string) {
	t.Helper()
	out, _ := utils.Run(kube.InnerCommand(kc, "-n", crNS, "get", "talosclusters.ichor.maesh.dev", c.Opts.Prefix, "-o", "jsonpath={.status.conditions[*].type}"))
	t.Logf("TalosCluster conditions: %s", out)
	if strings.Contains(out, "CNIHandover") {
		t.Errorf("a CNIHandover condition exists: %s", out)
	}
	if jobs, _ := utils.Run(kube.InnerCommand(kc, "-n", crNS, "get", "jobs", "--no-headers")); strings.Contains(jobs, "ichor-cni") {
		t.Errorf("an ichor-cni Job exists: %s", jobs)
	}
}

func flannelRunning(t *testing.T, c *foreign.Cluster, kc string) {
	t.Helper()
	pods, _ := utils.Run(kube.InnerCommand(kc, "-n", "kube-system", "get", "pods", "--no-headers"))
	t.Logf("kube-system pods:\n%s", pods)
	nodes, _ := utils.Run(kube.InnerCommand(kc, "get", "nodes", "-o", "wide", "--no-headers"))
	t.Logf("nodes:\n%s", nodes)
	if strings.Contains(nodes, "NotReady") {
		t.Errorf("a node is not Ready")
	}
}

// TestD4Talos: a Talos patch upgrade (ICHOR_E2E_F_UPGRADE_TO, default v1.14.4) through `ichor upgrade talos`.
func TestD4Talos(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	to := env("ICHOR_E2E_F_UPGRADE_TO", "v1.14.4")
	start := time.Now()
	if out, err := ichorCLI(t, c, "upgrade", "talos", to, "--yes"); err != nil {
		t.Fatalf("upgrade talos: %v\n%s", err, out)
	}
	waitOps(t, c, c.LocalKube, "talos", 9*time.Minute)
	t.Logf("Talos upgrade to %s took %s", to, time.Since(start).Round(time.Second))
	for _, ip := range c.Nodes() {
		v, err := talosctl.Version(ns, ip, ftc(t, c))
		t.Logf("talosctl version %s: %v\n%s", ip, err, v)
		if !strings.Contains(v, "Tag:"+" "+to) && !strings.Contains(v, to) {
			t.Errorf("node %s does not report %s", ip, to)
		}
	}
	noHandover(t, c, c.LocalKube)
	flannelRunning(t, c, c.LocalKube)
}

// TestD4K8s: a Kubernetes upgrade to the release set (ICHOR_E2E_F_K8S_TARGET, default v1.36.6). The
// product moves one minor at a time (pkg/upgrade kubernetes_skew.go), so the cluster starts on v1.35.10, not on the
// foreign default v1.34.12.
func TestD4K8s(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, K8sVersion: foreignK8sUpgradeStart})
	target := env("ICHOR_E2E_F_K8S_TARGET", "v1.36.6")
	start := time.Now()
	if out, err := ichorCLI(t, c, "upgrade", "kubernetes", target, "--yes"); err != nil {
		t.Fatalf("upgrade kubernetes: %v\n%s", err, out)
	}
	waitOps(t, c, c.LocalKube, "kubernetes", 9*time.Minute)
	t.Logf("Kubernetes upgrade to %s took %s", target, time.Since(start).Round(time.Second))
	vers, _ := inner(c, "get", "nodes", "-o", "jsonpath={.items[*].status.nodeInfo.kubeletVersion}")
	t.Logf("kubelet versions: %s", vers)
	for _, v := range strings.Fields(vers) {
		if v != target {
			t.Errorf("a node is on %s, not %s", v, target)
		}
	}
	noHandover(t, c, c.LocalKube)
	flannelRunning(t, c, c.LocalKube)
}

// TestD4RotateCA: `ichor cluster rotate-ca` (without --follow: the call returns after the credential hand-off).
func TestD4RotateCA(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	start := time.Now()
	out, err := ichorCLI(t, c, "cluster", "rotate-ca", c.Opts.Prefix, "--yes")
	t.Logf("rotate-ca returned after %s", time.Since(start).Round(time.Second))
	if err != nil {
		t.Fatalf("rotate-ca: %v\n%s", err, out)
	}
}

// rotatedKube copies the kubeconfig the CA rotation's hand-off wrote (next to /tmp/kubeconfig in the tools pod) and points
// it at the local relay tunnel.
func rotatedKube(t *testing.T, c *foreign.Cluster) string {
	t.Helper()
	pod, err := foreign.ToolsPod(c.Opts.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(c.Dir, "kubeconfig.rotated")
	if _, err := utils.Run(kube.Command("-n", ns, "cp", pod+":/tmp/kubeconfig.ichor-new", dst)); err != nil {
		t.Fatalf("copying the rotated kubeconfig: %v", err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	local := strings.ReplaceAll(string(raw), "https://"+c.CPs[0]+":6443", "https://127.0.0.1:"+c.Opts.RelayPort)
	if err := os.WriteFile(dst, []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestD4RotateCAWait polls the rotation (with the rotated credentials) until it Completes and checks the cluster afterwards.
func TestD4RotateCAWait(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	kc := rotatedKube(t, c)
	waitOps(t, c, kc, "ca-rotation", 9*time.Minute)
	noHandover(t, c, kc)
	flannelRunning(t, c, kc)
	if out, err := utils.Run(kube.InnerCommand(kc, "get", "--raw", "/readyz")); err != nil || !strings.Contains(out, "ok") {
		t.Errorf("readyz with the rotated kubeconfig: %v %s", err, out)
	}
}

// ichorBin is the workstation copy of the installembed ichor.
func ichorBin(t *testing.T) string { return repoPath(t, os.Getenv("ICHOR_E2E_F_ICHOR_BIN")) }

// TestD6: the CLI, the UI API and the webhook on an unmanaged (flannel) cluster.
func TestD6UnmanagedSurfaces(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	// CLI.
	out, err := ichorCLI(t, c, "cluster", "status", c.Opts.Prefix)
	if err != nil || !strings.Contains(out, "cni: unmanaged (flannel, not managed by Ichor)") {
		t.Errorf("cluster status lacks `cni: unmanaged (flannel, not managed by Ichor)` (err=%v)", err)
	}
	out, err = ichorCLI(t, c, "cni", "values", "get")
	if err == nil || !strings.Contains(out, unmanagedText) {
		t.Errorf("cni values get was not refused with the unmanaged text (err=%v)", err)
	}

	// UI API through the `ichor ui` port-forward.
	port := env("ICHOR_E2E_F_UI_PORT", "5144")
	ui := exec.Command(ichorBin(t), "ui", "--open-browser=false", "--local-port", port, "--kubeconfig", c.LocalKube, "-n", crNS) //nolint:gosec // test harness
	var uiOut strings.Builder
	ui.Stdout, ui.Stderr = &uiOut, &uiOut
	if err := ui.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ui.Process.Kill(); _, _ = ui.Process.Wait(); t.Logf("ichor ui output:\n%s", uiOut.String()) }()
	time.Sleep(15 * time.Second)
	root, err := foreign.Root()
	if err != nil {
		t.Fatal(err)
	}
	api := exec.Command("go", "run", "./test/harness/cniui", "-url", "https://127.0.0.1:"+port, "-cluster", c.Opts.Prefix) //nolint:gosec // test harness
	api.Dir = root
	apiOut, err := api.CombinedOutput()
	t.Logf("UI API (err=%v):\n%s", err, apiOut)
	s := string(apiOut)
	for _, want := range []string{`"cniPreset":"unmanaged"`, `"cniDetected":"flannel"`} {
		if !strings.Contains(s, want) {
			t.Errorf("GetCluster does not report %s", want)
		}
	}
	for _, rpc := range []string{"GetCiliumValues", "SetCiliumValues", "StartCniUpgrade"} {
		if !strings.Contains(s, rpc+": ERROR") {
			t.Errorf("%s was not refused", rpc)
		}
	}

	// Webhook: a copy of the cluster without the adoption annotation.
	raw, err := inner(c, "-n", crNS, "get", "talosclusters.ichor.maesh.dev", c.Opts.Prefix, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "status")
	obj["metadata"] = map[string]any{"name": "d6-refused", "namespace": crNS}
	b, _ := json.Marshal(obj)
	f := filepath.Join(t.TempDir(), "d6.json")
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cout, cerr := inner(c, "create", "-f", f)
	t.Logf("create TalosCluster preset unmanaged without the annotation: %v\n%s", cerr, cout)
	if cerr == nil || !strings.Contains(cout, "ichor.maesh.dev/adopted") {
		t.Errorf("the webhook did not refuse it naming the adoption annotation")
	}

	// ichor init has no unmanaged option.
	help, _ := exec.Command(ichorBin(t), "init", "--help").CombinedOutput() //nolint:gosec // test harness
	t.Logf("ichor init --help:\n%s", help)
	if strings.Contains(strings.ToLower(string(help)), "unmanaged") {
		t.Errorf("ichor init --help mentions unmanaged")
	}
}
