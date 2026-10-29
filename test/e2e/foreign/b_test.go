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

// Group B (whole-cluster adoption: B2 kube-proxy matrix, B3 secrets, B4 KubePrism, B5 older contract,
// B6 hold, B7 class C inline manifest). Each test is one foreground step against a cluster `foreign up` brought up
// (ICHOR_E2E_F_ATTACH=1, ICHOR_E2E_F_KEEP=1); the options passed to build must match the `foreign up` flags.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
)

const (
	twoOwners   = "two components own Service routing"
	noRouting   = "so nothing routes Services"
	canaryName  = "b3-canary"
	canaryValue = "b3-secret-value-written-before-adoption"
)

// bOpts are the harness options of a B-group cluster (they must match the `foreign up` flags).
func bOpts(cni string, noProxy, noPrism bool, sets ...string) foreign.Options {
	return foreign.Options{Workers: 1, CNI: cni, NoKubeProxy: noProxy, NoKubePrism: noPrism, CiliumSets: sets}
}

// adoptArgs runs `ichor adopt` in the tools pod with the given flags (no recovery flags are added) and returns the
// combined output and the exit error.
func adoptArgs(t *testing.T, c *foreign.Cluster, flags ...string) (string, error) {
	t.Helper()
	pod := stage(t, c)
	args := append([]string{"-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", "/tmp/ichor", "adopt",
		"--cluster", c.Opts.Prefix, "-n", crNS, "--talosconfig", "/tmp/talosconfig", "--kubeconfig", "/tmp/kubeconfig",
		"--node", c.CPs[0]}, flags...)
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", append([]string{"--context", os.Getenv(kube.ContextEnv)}, args...)...).CombinedOutput()
	t.Logf("ichor adopt %s: err=%v\n%s", strings.Join(flags, " "), err, out)
	return string(out), err
}

func recoverFlags(c *foreign.Cluster) []string {
	return []string{"--recover-secrets", "--recover-from", c.CPs[0]}
}

// nothingWritten asserts the ichor-system namespace and Ichor's CRDs do not exist on the foreign cluster.
func nothingWritten(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if got, err := inner(c, "get", "namespace", crNS, "--no-headers"); err == nil {
		t.Errorf("namespace %s exists after a refusal: %s", crNS, got)
	}
	if got, _ := inner(c, "get", "crd", "--no-headers"); strings.Contains(got, "ichor.maesh.dev") {
		t.Errorf("Ichor CRDs exist after a refusal")
	}
}

// helmSet changes the Cilium release the harness installed (reuse values) and waits for the DaemonSet.
func helmSet(t *testing.T, c *foreign.Cluster, sets ...string) {
	t.Helper()
	args := []string{"upgrade", "cilium", repoPath(t, "pkg/cni/cilium/chart/cilium-1.18.0.tgz"), "--kubeconfig", c.LocalKube,
		"--namespace", "kube-system", "--reuse-values"}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm upgrade: %v\n%s", err, out)
	}
	if o, err := inner(c, "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=300s"); err != nil {
		t.Fatalf("cilium rollout: %v\n%s", err, o)
	}
	if o, err := inner(c, "-n", "kube-system", "rollout", "status", "deploy/cilium-operator", "--timeout=300s"); err != nil {
		t.Fatalf("cilium-operator rollout: %v\n%s", err, o)
	}
	kpr, _ := inner(c, "-n", "kube-system", "get", "cm", "cilium-config", "-o", "jsonpath={.data.kube-proxy-replacement}")
	t.Logf("cilium-config kube-proxy-replacement = %q after helm upgrade %v", strings.TrimSpace(kpr), sets)
}

// refusedUntouched runs plan and the real adopt and asserts both fail with want and nothing was written.
func refusedUntouched(t *testing.T, c *foreign.Cluster, want string) {
	t.Helper()
	before := cniPaths(t, c)
	for _, extra := range [][]string{{"--plan"}, {"--yes"}} {
		out, err := adoptArgs(t, c, append(recoverFlags(c), extra...)...)
		if err == nil {
			t.Errorf("adopt %v succeeded, want a refusal containing %q", extra, want)
		}
		if !strings.Contains(out, want) {
			t.Errorf("adopt %v refusal lacks %q", extra, want)
		}
	}
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
}

// kubeProxyMode reads spec.cni.kubeProxy of the adopted cluster.
func kubeProxyMode(c *foreign.Cluster) string { return clusterJSONPath(c, "{.spec.cni.kubeProxy}") }

// TestB2RefusedEnabledReplacementOn (B2): kube-proxy runs in Talos and Cilium replaces it.
// Cluster: cilium-helm, --no-kubeprism, --cilium-set kubeProxyReplacement=true.
func TestB2RefusedEnabledReplacementOn(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, false, true, "kubeProxyReplacement=true"))
	kpr, _ := inner(c, "-n", "kube-system", "get", "cm", "cilium-config", "-o", "jsonpath={.data.kube-proxy-replacement}")
	t.Logf("cilium-config kube-proxy-replacement = %q", strings.TrimSpace(kpr))
	refusedUntouched(t, c, twoOwners)
}

// TestB2RefusedDisabledReplacementOff (B2): kube-proxy is disabled in Talos and Cilium does not replace it.
// Cluster: cilium-helm, --no-kube-proxy, --cilium-set kubeProxyReplacement=false.
func TestB2RefusedDisabledReplacementOff(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, true, false, "kubeProxyReplacement=false"))
	refusedUntouched(t, c, noRouting)
}

// TestB4RefusedReplacedKubePrismOff (B4): replaced mode with KubePrism off is refused.
// Cluster: cilium-helm, --no-kube-proxy --no-kubeprism (the harness then points Cilium at the control plane).
func TestB4RefusedReplacedKubePrismOff(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, true, true))
	refusedUntouched(t, c, "KubePrism is off on machine")
}

// TestHelmSetKPR changes kubeProxyReplacement on the running Cilium: ICHOR_E2E_F_KPR=true|false.
func TestHelmSetKPR(t *testing.T) {
	v := os.Getenv("ICHOR_E2E_F_KPR")
	if v == "" {
		t.Skip("ICHOR_E2E_F_KPR not set")
	}
	c := build(t, bOpts(foreign.CNICiliumHelm, false, false))
	helmSet(t, c, "kubeProxyReplacement="+v)
}

// kubePrismState returns, per node, whether the live machine config enables KubePrism.
func kubePrismState(t *testing.T, c *foreign.Cluster) map[string]string {
	t.Helper()
	res := map[string]string{}
	for _, ip := range append(append([]string{}, c.CPs...), c.Workers...) {
		doc, kubeDocs := configDocs(t, c, ip)
		m, _ := doc["machine"].(map[string]any)
		feat, _ := m["features"].(map[string]any)
		kp := ""
		if v, ok := feat["kubePrism"]; ok {
			kp = "features:" + strings.TrimSpace(stringify(v))
		}
		for _, d := range kubeDocs {
			if mm, ok := d.(map[string]any); ok && mm["kind"] == "KubePrismConfig" {
				kp += " doc:" + strings.TrimSpace(stringify(mm))
			}
		}
		res[ip] = kp
	}
	return res
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// machineConfigVersions returns the resource version of the machineconfig on each node: it changes on every apply.
func machineConfigVersions(t *testing.T, c *foreign.Cluster) map[string]string {
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
		objs, _ := talosctl.DecodeAll(out)
		if len(objs) > 0 {
			md, _ := objs[0]["metadata"].(map[string]any)
			res[ip] = fmt.Sprint(md["version"])
		}
	}
	return res
}

