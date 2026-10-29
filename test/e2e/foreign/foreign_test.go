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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	c, err := foreign.Up(o)
	if err != nil {
		t.Fatalf("foreign.Up: %v", err)
	}
	return c
}

// cniPaths returns, per node, the canonical JSON of the machine-config paths that decide the CNI.
func cniPaths(t *testing.T, c *foreign.Cluster) map[string]string {
	t.Helper()
	tc, err := os.ReadFile(c.Talosconfig)
	if err != nil {
		t.Fatal(err)
	}
	res := map[string]string{}
	for _, ip := range append(append([]string{}, c.CPs...), c.Workers...) {
		out, err := talosctl.GetAuthenticated(ns, ip, "machineconfig", tc)
		if err != nil {
			t.Fatalf("machineconfig %s: %v", ip, err)
		}
		objs, err := talosctl.DecodeAll(out)
		if err != nil || len(objs) == 0 {
			t.Fatalf("decoding machineconfig %s: %v", ip, err)
		}
		var doc map[string]any
		switch s := objs[0]["spec"].(type) {
		case string:
			for _, part := range strings.Split(s, "\n---\n") {
				var d map[string]any
				if yaml.Unmarshal([]byte(part), &d) == nil && d["machine"] != nil {
					doc = d
				}
			}
		case map[string]any:
			doc = s
		}
		if doc == nil {
			t.Fatalf("no v1alpha1 document in machineconfig of %s", ip)
		}
		cl, _ := doc["cluster"].(map[string]any)
		m, _ := doc["machine"].(map[string]any)
		feat, _ := m["features"].(map[string]any)
		pick := map[string]any{
			"network": cl["network"], "proxy": cl["proxy"], "inlineManifests": cl["inlineManifests"],
			"extraManifests": cl["extraManifests"], "kubePrism": feat["kubePrism"],
		}
		b, _ := json.Marshal(pick)
		res[ip] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	return res
}

// adopt runs `ichor adopt` inside the tools pod (the workstation cannot reach pod IPs) and
// returns its combined output and exit error.
func adopt(t *testing.T, c *foreign.Cluster, extra ...string) (string, error) {
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
	args := append([]string{"-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", "/tmp/ichor", "adopt",
		"--cluster", c.Opts.Prefix, "-n", crNS, "--talosconfig", "/tmp/talosconfig", "--kubeconfig", "/tmp/kubeconfig",
		"--recover-secrets", "--recover-from", c.CPs[0], "--node", c.CPs[0], "--yes"}, extra...)
	// kube.Command refuses a "--cluster" argument anywhere; here it belongs to the command run
	// inside the pod, so the context is pinned by hand to the same ICHOR_E2E_KUBE_CONTEXT.
	cmd := exec.Command("kubectl", append([]string{"--context", os.Getenv(kube.ContextEnv)}, args...)...)
	out, err := cmd.CombinedOutput()
	// ponytail: the full output is printed; it carries no keys (adopt prints nothing secret).
	t.Logf("ichor adopt %s:\n%s", strings.Join(extra, " "), out)
	return string(out), err
}

func inner(c *foreign.Cluster, args ...string) (string, error) {
	return utils.Run(kube.InnerCommand(c.LocalKube, args...))
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
	if got, _ := inner(c, "-n", crNS, "get", "talosclusters.ichor.maesh.dev", c.Opts.Prefix, "-o", "jsonpath={.spec.cni.preset}"); got != "unmanaged" {
		t.Errorf("spec.cni.preset = %q, want unmanaged", got)
	}
	held, _ := inner(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev", "-o",
		"jsonpath={.items[*].metadata.annotations.ichor\\.maesh\\.dev/hold-apply}")
	if strings.TrimSpace(held) != "" {
		t.Errorf("hold-apply still set: %q", held)
	}
	jobs, _ := inner(c, "-n", crNS, "get", "jobs", "--no-headers")
	if strings.Contains(jobs, "ichor-cni") {
		t.Errorf("an ichor-cni Job exists: %s", jobs)
	}
	if got, _ := inner(c, "-n", crNS, "get", "talosclusters.ichor.maesh.dev", c.Opts.Prefix, "-o",
		"jsonpath={.status.conditions[?(@.type==\"CNIHandover\")].status}"); got != "" {
		t.Errorf("CNIHandover condition present: %q", got)
	}
	if p, _ := inner(c, "get", "ciliumnetworkpolicies.cilium.io", "-A", "--no-headers"); strings.TrimSpace(p) != "" {
		t.Errorf("Cilium policies exist on a flannel cluster: %s", p)
	}
	// Give the controller time to render and apply (it must change nothing CNI-related).
	time.Sleep(3 * time.Minute)
	sameHashes(t, before, cniPaths(t, c))
	if nodes, _ := inner(c, "get", "nodes", "--no-headers"); strings.Contains(nodes, "NotReady") {
		t.Errorf("nodes not Ready after adoption: %s", nodes)
	}
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
	// Ichor is not installed, so its kinds are not served; the namespace must hold nothing either.
	if got, err := inner(c, "get", "namespace", crNS, "--no-headers"); err == nil {
		t.Errorf("namespace %s exists after a refusal: %s", crNS, got)
	}
	if got, _ := inner(c, "get", "crd", "--no-headers"); strings.Contains(got, "ichor.maesh.dev") {
		t.Errorf("Ichor CRDs exist after a refusal: %s", got)
	}
	// The plan phase writes nothing anywhere, not even the recovered secrets bundle.
	if pod, err := foreign.ToolsPod(c.Opts.Prefix); err == nil {
		if got, err := utils.Run(kube.Command("-n", ns, "exec", pod, "--", "ls", "/tmp/home/.ichor")); err == nil {
			t.Errorf("a refused adopt left files under ~/.ichor: %s", got)
		}
	}
}

// The rest of the matrix: stubs until their units land.
func TestA1(t *testing.T)   { t.Skip("A1 cilium inline manifest takeover") }
func TestA2(t *testing.T)   { t.Skip("A2 helm install cilium takeover") }
func TestB8(t *testing.T)   { t.Skip("B8 Talos older than v1.12 refused") }
func TestC1(t *testing.T)   { t.Skip("C1 reclaim a worker of another cluster") }
func TestD2(t *testing.T)   { t.Skip("D2 calico adopted unmanaged") }
func TestD3(t *testing.T)   { t.Skip("D3 cilium adopted unmanaged") }
func TestD5(t *testing.T)   { t.Skip("D5 cni none adopted with talosCNI none") }
func TestB2B4(t *testing.T) { t.Skip("B2 kube-proxy matrix, B4 KubePrism") }
