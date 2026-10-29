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

// Package foreign holds the hardware cases that adopt or reclaim clusters Ichor did
// not create (groups B, C, D). Run one case at a time:
//
//	go test -tags=e2e ./test/e2e/foreign/ -run TestD1 -v -timeout 90m
//
// Needs ICHOR_E2E_KUBE_CONTEXT, ICHOR_E2E_F_ICHOR_BIN (the installembed ichor that
// `make demo-images` builds, bin/img/ichor/ichor) and the images pushed to the registry
// `foreign registry` forwards (default localhost:5014). ICHOR_E2E_F_KEEP=1 skips teardown.
package foreign

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	yaml "go.yaml.in/yaml/v4"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const (
	ns      = "ichor-e2e"
	crNS    = "ichor-system"
	prefixE = "ICHOR_E2E_F_PREFIX"
)

// repoPath resolves a repository-relative path (the harness helpers run from the repo root).
func repoPath(t *testing.T, p string) string {
	t.Helper()
	if filepath.IsAbs(p) {
		return p
	}
	root, err := foreign.Root()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, p)
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// build brings up a foreign cluster and registers its teardown.
func build(t *testing.T, o foreign.Options) *foreign.Cluster {
	t.Helper()
	if os.Getenv(kube.ContextEnv) == "" || os.Getenv("ICHOR_E2E_F_ICHOR_BIN") == "" {
		t.Skip(kube.ContextEnv + " and ICHOR_E2E_F_ICHOR_BIN must be set")
	}
	o.Prefix = env(prefixE, foreign.PrefixRoot)
	o.TalosVersion = env("ICHOR_E2E_F_TALOS", o.TalosVersion)
	o.RegistryMirror = true
	o.RelayPort = env("ICHOR_E2E_F_RELAY_PORT", o.RelayPort)
	o.CiliumChart = repoPath(t, "pkg/cni/cilium/chart/cilium-1.18.0.tgz")
	if !strings.HasPrefix(o.Prefix, foreign.PrefixRoot) {
		t.Fatal("bad prefix")
	}
	if os.Getenv("ICHOR_E2E_F_KEEP") != "1" {
		t.Cleanup(func() {
			if err := foreign.Down(o.Prefix, false); err != nil {
				t.Errorf("teardown: %v", err)
			}
		})
	}
	up := foreign.Up
	if os.Getenv("ICHOR_E2E_F_ATTACH") == "1" { // the cluster was brought up by `go run ./test/harness/foreign up`
		up = foreign.Attach
	}
	c, err := up(o)
	if err != nil {
		t.Fatalf("foreign.Up: %v", err)
	}
	return c
}

// configDocs returns the v1alpha1 document and the Kube* documents of a node's machine config.
func configDocs(t *testing.T, c *foreign.Cluster, ip string) (doc map[string]any, kubeDocs []any) {
	t.Helper()
	out, err := talosctl.GetAuthenticated(ns, ip, "machineconfig", ftc(t, c))
	if err != nil {
		t.Fatalf("machineconfig %s: %v", ip, err)
	}
	objs, err := talosctl.DecodeAll(out)
	if err != nil || len(objs) == 0 {
		t.Fatalf("decoding machineconfig %s: %v", ip, err)
	}
	switch s := objs[0]["spec"].(type) {
	case string: // the v1.14 contract keeps network, CNI, proxy and inline manifests in their own documents
		for _, part := range strings.Split(s, "\n---\n") {
			var d map[string]any
			if yaml.Unmarshal([]byte(part), &d) != nil {
				continue
			}
			if d["machine"] != nil {
				doc = d
			} else if k, _ := d["kind"].(string); strings.HasPrefix(k, "Kube") {
				kubeDocs = append(kubeDocs, d)
			}
		}
	case map[string]any:
		doc = s
	}
	if doc == nil {
		t.Fatalf("no v1alpha1 document in machineconfig of %s", ip)
	}
	return doc, kubeDocs
}

// cniPaths returns, per node, the canonical JSON of the machine-config paths that decide the CNI.
func cniPaths(t *testing.T, c *foreign.Cluster) map[string]string {
	t.Helper()
	res := map[string]string{}
	for _, ip := range c.Nodes() {
		doc, kubeDocs := configDocs(t, c, ip)
		cl, _ := doc["cluster"].(map[string]any)
		m, _ := doc["machine"].(map[string]any)
		feat, _ := m["features"].(map[string]any)
		pick := map[string]any{
			"network": cl["network"], "proxy": cl["proxy"], "inlineManifests": cl["inlineManifests"],
			"extraManifests": cl["extraManifests"], "kubePrism": feat["kubePrism"], "kubeDocs": kubeDocs,
		}
		b, _ := json.Marshal(pick)
		res[ip] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return res
}

// stage copies ichor, the talosconfig and the kubeconfig into the tools pod and returns its name.
func stage(t *testing.T, c *foreign.Cluster) string {
	t.Helper()
	pod, err := foreign.ToolsPod(c.Opts.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range [][2]string{
		{repoPath(t, os.Getenv("ICHOR_E2E_F_ICHOR_BIN")), "/tmp/ichor"}, {c.Talosconfig, "/tmp/talosconfig"}, {c.Kubeconfig, "/tmp/kubeconfig"},
	} {
		if _, err := utils.Run(kube.Command("-n", ns, "cp", cp[0], pod+":"+cp[1])); err != nil {
			t.Fatalf("copying %s: %v", cp[1], err)
		}
	}
	return pod
}

// ichorCLI runs `ichor <args> --cluster <prefix> -n ichor-system --local` in the tools pod (the CLI computes from the
// Kubernetes API directly: ichor-ui's certificate has no in-cluster name).
func ichorCLI(t *testing.T, c *foreign.Cluster, args ...string) (string, error) {
	t.Helper()
	pod := stage(t, c)
	a := append([]string{"-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", "/tmp/ichor"}, args...)
	if args[0] != "cluster" { // `ichor cluster ...` takes the name as an argument and has no --cluster flag
		a = append(a, "--cluster", c.Opts.Prefix)
	}
	a = append(a, "-n", crNS, "--kubeconfig", "/tmp/kubeconfig", "--local")
	out, err := exec.Command("kubectl", append([]string{"--context", os.Getenv(kube.ContextEnv)}, a...)...).CombinedOutput()
	t.Logf("ichor %s:\n%s", strings.Join(args, " "), out)
	return string(out), err
}

// adopt runs `ichor adopt` inside the tools pod (the workstation cannot reach pod IPs) and
// returns its combined output and exit error.
func adopt(t *testing.T, c *foreign.Cluster, extra ...string) (string, error) {
	t.Helper()
	return adoptBin(t, c, "/tmp/ichor", extra...)
}

// adoptBin is adopt with the ichor binary at bin inside the tools pod (stage puts ICHOR_E2E_F_ICHOR_BIN at /tmp/ichor).
func adoptBin(t *testing.T, c *foreign.Cluster, bin string, extra ...string) (string, error) {
	t.Helper()
	out, _, _, err := adoptTriggered(t, c, bin, "", 0, nil, extra...)
	return out, err
}

// adoptArgs runs `ichor adopt` with exactly the given flags (no --yes, no recovery flags added).
func adoptArgs(t *testing.T, c *foreign.Cluster, flags ...string) (string, error) {
	t.Helper()
	out, _, _, err := adoptRun(t, c, "/tmp/ichor", flags, "", 0, nil)
	return out, err
}

// adoptTriggered runs `ichor adopt --yes` (the binary at bin in the tools pod, recovery flags added) and runs action delay
// after its output first contains trigger (never when trigger is empty). It returns the output, how long adopt ran, whether
// the action ran, and adopt's error.
func adoptTriggered(t *testing.T, c *foreign.Cluster, bin, trigger string, delay time.Duration, action func(), extra ...string) (string, time.Duration, bool, error) {
	t.Helper()
	return adoptRun(t, c, bin, append([]string{"--yes"}, append(recoverArgs(c, extra), extra...)...), trigger, delay, action)
}

func adoptRun(t *testing.T, c *foreign.Cluster, bin string, flags []string, trigger string, delay time.Duration, action func()) (string, time.Duration, bool, error) {
	t.Helper()
	pod := stage(t, c)
	args := append([]string{"-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", bin, "adopt",
		"--cluster", c.Opts.Prefix, "-n", crNS, "--talosconfig", "/tmp/talosconfig", "--kubeconfig", "/tmp/kubeconfig",
		"--node", c.CPs[0]}, flags...)
	// kube.Command refuses a "--cluster" argument anywhere; here it belongs to the command run
	// inside the pod, so the context is pinned by hand to the same ICHOR_E2E_KUBE_CONTEXT.
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute) // adopt waits for Ichor's webhooks without a bound (D5: no CNI, never ready)
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
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		mu.Lock()
		out.WriteString(line + "\n")
		mu.Unlock()
		t.Logf("%s [%s]", line, time.Now().Format("15:04:05")) // the output carries no keys (adopt prints nothing secret)
		if !ran && trigger != "" && strings.Contains(line, trigger) {
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

// recoverArgs: a rerun that passes --secrets-bundle (the bundle an earlier run wrote) must not recover the secrets again.
func recoverArgs(c *foreign.Cluster, extra []string) []string {
	if slices.Contains(extra, "--secrets-bundle") {
		return nil
	}
	return recoverFlags(c)
}

func inner(c *foreign.Cluster, args ...string) (string, error) {
	return utils.Run(kube.InnerCommand(c.LocalKube, args...))
}

// holdCleared asserts no TalosMachine still carries the hold-apply annotation.
func holdCleared(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	held, _ := inner(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o",
		"jsonpath={.items[*].metadata.annotations.ichor\\.maesh\\.dev/hold-apply}")
	if strings.TrimSpace(held) != "" {
		t.Errorf("hold-apply still set: %q", held)
	}
}

// noCNIJob asserts the controller created no ichor-cni Job.
func noCNIJob(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if jobs, _ := inner(c, "-n", crNS, "get", "jobs", "--no-headers"); strings.Contains(jobs, "ichor-cni") {
		t.Errorf("an ichor-cni Job exists: %s", jobs)
	}
}

// nodesReady asserts no Node of the foreign cluster is NotReady.
func nodesReady(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if nodes, _ := inner(c, "get", "nodes", "--no-headers"); strings.Contains(nodes, "NotReady") {
		t.Errorf("nodes not Ready: %s", nodes)
	}
}

func sameHashes(t *testing.T, before, after map[string]string) {
	t.Helper()
	for ip, h := range before {
		if after[ip] != h {
			t.Errorf("CNI-relevant machine config of %s changed", ip)
		}
	}
}

// TestD1: a Talos-flannel cluster is adopted with --cni-unmanaged.
func TestD1FlannelAdoptedUnmanaged(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	before := cniPaths(t, c)
	out, err := adopt(t, c, "--cni-unmanaged")
	if err != nil {
		t.Fatalf("adopt --cni-unmanaged failed: %v\n%s", err, out)
	}
	unmanagedAdopted(t, c) // also: adopt must never silently adopt a subset of the cluster
	holdCleared(t, c)
	// kubectl exits non-zero when the Cilium CRD is not served (the expected state): only a successful, non-empty listing is a failure.
	if p, err := inner(c, "get", "ciliumnetworkpolicies.cilium.io", "-A", "--no-headers"); err == nil && strings.TrimSpace(p) != "" {
		t.Errorf("Cilium policies exist on a flannel cluster: %s", p)
	}
	// Give the controller time to render and apply (it must change nothing CNI-related).
	time.Sleep(3 * time.Minute)
	sameHashes(t, before, cniPaths(t, c))
	nodesReady(t, c)
}

// TestB1: the same cluster without the flag is refused and nothing is written.
func TestB1FlannelRefusedWithoutFlag(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	before := cniPaths(t, c)
	out, err := adopt(t, c)
	if err == nil {
		t.Fatal("adopt without --cni-unmanaged succeeded on a flannel cluster")
	}
	if !strings.Contains(out, "--cni-unmanaged") {
		t.Errorf("refusal does not name --cni-unmanaged: %s", out)
	}
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
	// The plan phase writes nothing anywhere, not even the recovered secrets bundle.
	if pod, err := foreign.ToolsPod(c.Opts.Prefix); err == nil {
		if got, err := utils.Run(kube.Command("-n", ns, "exec", pod, "--", "ls", "/tmp/home/.ichor")); err == nil {
			t.Errorf("a refused adopt left files under ~/.ichor: %s", got)
		}
	}
}

// names lists the metadata.name of a resource of the foreign cluster.
func names(c *foreign.Cluster, args ...string) []string {
	out, _ := inner(c, append(args, "-o", "jsonpath={.items[*].metadata.name}")...)
	return strings.Fields(out)
}

// membership waits for one TalosMachine per Node (adopt may return before the last one is written).
func membership(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	var machines, nodes int
	if _, err := kube.Poll("TalosMachines == Nodes", 3*time.Minute, func() (string, bool) {
		machines = len(names(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev"))
		nodes = len(names(c, "get", "nodes"))
		return "", machines == nodes && nodes > 0
	}); err != nil {
		t.Errorf("TalosMachines (%d) != Nodes (%d) after 3 minutes", machines, nodes)
	}
}

func clusterJSONPath(c *foreign.Cluster, path string) string {
	out, _ := inner(c, "-n", crNS, "get", "talosclusters.ichor.maesh.dev", c.Opts.Prefix, "-o", "jsonpath="+path)
	return strings.TrimSpace(out)
}

// waitCluster polls a TalosCluster jsonpath until it equals want.
func waitCluster(t *testing.T, c *foreign.Cluster, path, want string) {
	t.Helper()
	var got string
	if _, err := kube.Poll("TalosCluster "+path, 5*time.Minute, func() (string, bool) {
		got = clusterJSONPath(c, path)
		return got, got == want
	}); err != nil {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

// unmanagedAdopted asserts what every --cni-unmanaged adoption shares.
func unmanagedAdopted(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if got := clusterJSONPath(c, "{.spec.cni.preset}"); got != "unmanaged" {
		t.Errorf("spec.cni.preset = %q, want unmanaged", got)
	}
	if jobs, _ := inner(c, "-n", crNS, "get", "jobs", "--no-headers"); strings.Contains(jobs, "ichor-cni") {
		t.Errorf("an ichor-cni Job exists: %s", jobs)
	}
	if got := clusterJSONPath(c, "{.status.conditions[?(@.type==\"CNIHandover\")].status}"); got != "" {
		t.Errorf("CNIHandover condition present: %q", got)
	}
	membership(t, c)
}

// TestD2: a Calico cluster is adopted with --cni-unmanaged.
func TestD2CalicoAdoptedUnmanaged(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICalico})
	before := cniPaths(t, c)
	if out, err := adopt(t, c, "--cni-unmanaged"); err != nil {
		t.Fatalf("adopt --cni-unmanaged failed: %v\n%s", err, out)
	}
	unmanagedAdopted(t, c)
	waitCluster(t, c, "{.status.cni.detected}", "calico")
	waitCluster(t, c, "{.status.conditions[?(@.type==\"NetworkPoliciesApplied\")].status}", "False")
	waitCluster(t, c, "{.status.conditions[?(@.type==\"NetworkPoliciesApplied\")].reason}", "CNINotCilium")
	time.Sleep(3 * time.Minute)
	sameHashes(t, before, cniPaths(t, c))
	nodesReady(t, c)
}

const unmanagedText = "the CNI is not managed by Ichor (spec.cni.preset: unmanaged)"

// TestD3: a Cilium (Helm) cluster is adopted with --cni-unmanaged; Ichor touches no release record.
func TestD3CiliumAdoptedUnmanaged(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	records := func() string {
		out, _ := inner(c, "-n", "kube-system", "get", "secrets", "-l", "owner=helm", "-o",
			"jsonpath={range .items[*]}{.metadata.name} {.metadata.resourceVersion}{\"\\n\"}{end}")
		return out
	}
	before, hashes := records(), cniPaths(t, c)
	if strings.TrimSpace(before) == "" {
		t.Fatal("no Helm release record before adoption")
	}
	if out, err := adopt(t, c, "--cni-unmanaged"); err != nil {
		t.Fatalf("adopt --cni-unmanaged failed: %v\n%s", err, out)
	}
	unmanagedAdopted(t, c)
	waitCluster(t, c, "{.status.cni.detected}", "cilium")
	time.Sleep(3 * time.Minute)
	if after := records(); after != before {
		t.Errorf("Helm release records changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	sameHashes(t, hashes, cniPaths(t, c))
	if out, err := ichorCLI(t, c, "cni", "values", "set", "-f", "/dev/null"); err == nil || !strings.Contains(out, unmanagedText) {
		t.Errorf("cni values set was not refused with the unmanaged text (err=%v)", err)
	}
	op := filepath.Join(t.TempDir(), "op.yaml")
	manifest := "apiVersion: ichor.maesh.dev/v1alpha1\nkind: TalosOperation\nmetadata:\n  name: d3-cni\n  namespace: " + crNS +
		"\nspec:\n  kind: cni\n  clusterRef:\n    name: " + c.Opts.Prefix + "\n"
	if err := os.WriteFile(op, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	cout, cerr := inner(c, "create", "-f", op)
	t.Logf("create cni TalosOperation: %v\n%s", cerr, cout)
	if cerr == nil || !strings.Contains(cout, unmanagedText) {
		t.Errorf("the cni TalosOperation was not refused with the unmanaged text")
	}
}

// TestB8: Talos older than v1.12 is refused with the upgrade message and nothing is written.
// ICHOR_E2E_F_TALOS defaults to v1.11.5 here.
func TestB8TalosTooOldRefused(t *testing.T) {
	if os.Getenv("ICHOR_E2E_F_TALOS") == "" {
		t.Setenv("ICHOR_E2E_F_TALOS", "v1.11.5")
	}
	c := build(t, foreign.Options{Workers: 0})
	before := cniPaths(t, c)
	out, err := adopt(t, c, "--cni-unmanaged")
	if err == nil {
		t.Fatal("adopt succeeded on a Talos older than v1.12")
	}
	if !strings.Contains(out, "older than the oldest version Ichor adopts (v1.12)") {
		t.Errorf("refusal does not carry the upgrade message: %s", out)
	}
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
}
