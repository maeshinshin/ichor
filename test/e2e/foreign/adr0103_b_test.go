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

// ADR 0103 group B (whole-cluster adoption: B2 kube-proxy matrix, B3 secrets, B4 KubePrism, B5 older contract,
// B6 hold, B7 class C inline manifest). Each test is one foreground step against a cluster `foreign up` brought up
// (ICHOR_E2E_F_ATTACH=1, ICHOR_E2E_F_KEEP=1); the options passed to build must match the `foreign up` flags.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
	"github.com/maeshinshin/ichor/test/internal/talosctl"
	"github.com/maeshinshin/ichor/test/utils"
)

const (
	twoOwners   = "two components own Service routing"
	noRouting   = "so nothing routes Services"
	canaryName  = "b3-canary"
	canaryValue = "b3-secret-value-written-before-adoption"
)

// bOpts are the harness options of a B-group cluster (they must match the `foreign up` flags).
func bOpts(cni string, noProxy, noPrism bool, sets ...string) foreign.Options {
	w := 1
	if os.Getenv("ICHOR_E2E_F_WORKERS") == "0" {
		w = 0
	}
	o := foreign.Options{Workers: w, CNI: cni, NoKubeProxy: noProxy, NoKubePrism: noPrism, CiliumSets: sets}
	return o
}

func recoverFlags(c *foreign.Cluster) []string {
	if b := os.Getenv("ICHOR_E2E_F_BUNDLE"); b != "" { // a rerun after adopt wrote the recovered bundle
		return []string{"--secrets-bundle", b}
	}
	return []string{"--recover-secrets", "--recover-from", c.CPs[0]}
}

// nothingWritten asserts the ichor-system namespace and Ichor's CRDs do not exist on the foreign cluster.
func nothingWritten(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if got, err := inner(c, "get", "namespace", crNS, "--no-headers"); err == nil {
		t.Errorf("namespace %s exists after a refusal: %s", crNS, got)
	}
	if got, _ := inner(c, "get", "crd", "--no-headers"); strings.Contains(got, "ichor.maesh.dev") {
		t.Errorf("Ichor CRDs exist after a refusal: %s", got)
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
	// A ConfigMap change alone does not restart the agents; the data plane only changes when they do.
	for _, w := range []string{"ds/cilium", "ds/cilium-envoy", "deploy/cilium-operator"} {
		if o, err := inner(c, "-n", "kube-system", "rollout", "restart", w); err != nil {
			t.Fatalf("restart %s: %v\n%s", w, err, o)
		}
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

// TestHelmSet changes values on the running Cilium: ICHOR_E2E_F_HELMSET=kubeProxyReplacement=false,tunnelProtocol=vxlan.
func TestHelmSet(t *testing.T) {
	v := os.Getenv("ICHOR_E2E_F_HELMSET")
	if v == "" {
		t.Skip("ICHOR_E2E_F_HELMSET not set")
	}
	c := build(t, bOpts(foreign.CNICiliumHelm, false, false))
	helmSet(t, c, strings.Split(v, ",")...)
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

// b3Canary creates the Kubernetes Secret that must stay readable after adoption (it is encrypted at rest with the
// cluster's secretbox key, which the recovered bundle must reproduce).
func b3Canary(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	if out, err := inner(c, "-n", "default", "create", "secret", "generic", canaryName, "--from-literal=v="+canaryValue); err != nil && !strings.Contains(out, "already exists") {
		t.Fatalf("creating the canary Secret: %v\n%s", err, out)
	}
}

func b3CanaryReadable(t *testing.T, c *foreign.Cluster) {
	t.Helper()
	out, err := inner(c, "-n", "default", "get", "secret", canaryName, "-o", "jsonpath={.data.v}")
	if err != nil || out == "" {
		t.Errorf("canary Secret not readable: %v %q", err, out)
		return
	}
	t.Logf("canary Secret readable after adoption (%d bytes of base64)", len(out))
}

// TestB3WrongBundle (B3): a bundle of another cluster produces secret-field drift and no patch. Runs before the real adopt.
func TestB3WrongBundle(t *testing.T) {
	c := build(t, bOpts(foreign.CNICiliumHelm, false, true, "kubeProxyReplacement=true"))
	b3Canary(t, c)
	wrong := filepath.Join(t.TempDir(), "wrong-secrets.yaml")
	if out, err := exec.Command("talosctl", "gen", "secrets", "-o", wrong).CombinedOutput(); err != nil {
		t.Fatalf("talosctl gen secrets: %v\n%s", err, out)
	}
	pod := stage(t, c)
	if out, err := utils.Run(kube.Command("-n", ns, "cp", wrong, pod+":/tmp/wrong-secrets.yaml")); err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
	before := cniPaths(t, c)
	for _, extra := range []string{"--plan", "--yes"} {
		out, err := adoptArgs(t, c, "--secrets-bundle", "/tmp/wrong-secrets.yaml", extra)
		if err == nil {
			t.Errorf("wrong bundle %s: adopt succeeded", extra)
		}
		for _, want := range []string{"machine.ca.key", "cluster.token", "nothing was written"} {
			if !strings.Contains(out, want) {
				t.Errorf("wrong bundle %s: output lacks %q", extra, want)
			}
		}
	}
	sameHashes(t, before, cniPaths(t, c))
	if got := clusterJSONPath(c, "{.metadata.name}"); got != "" {
		t.Logf("a TalosCluster was written by the wrong-bundle adopt: %s", got)
	}
	ms, _ := inner(c, "-n", crNS, "get", "talosconfigpatches.ichor.maesh.dev,talosmachines.ichor.maesh.dev", "-o", "name")
	t.Logf("objects after the wrong-bundle adopt:\n%s", ms)
}
