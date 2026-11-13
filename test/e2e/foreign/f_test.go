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

// Ichor installed with Helm from the packaged chart (`make helm-package APP_VERSION=<tag>`,
// bin/chart/ichor-*.tgz; the images are in the registry the nodes mirror ghcr.io to).

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// local runs a workstation command against the foreign cluster (its kubeconfig is passed by the caller) and logs it.
func local(t *testing.T, d time.Duration, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed tool names from this file
	t.Logf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	return string(out), err
}

// packagedChart is the one chart archive in bin/chart.
func packagedChart(t *testing.T) string {
	t.Helper()
	charts, _ := filepath.Glob(repoPath(t, "bin/chart/ichor-*.tgz"))
	if len(charts) != 1 {
		t.Fatalf("want exactly one packaged chart in bin/chart (make helm-package APP_VERSION=<pushed tag>), got %v", charts)
	}
	return charts[0]
}

// helmInstall runs `helm install ichor <packaged chart>` against the foreign cluster.
func helmInstall(t *testing.T, c *foreign.Cluster, wait bool) (string, error) {
	t.Helper()
	args := []string{"install", "ichor", packagedChart(t), "--kubeconfig", c.LocalKube, "-n", crNS, "--create-namespace"}
	if wait {
		args = append(args, "--wait", "--timeout", "6m")
	}
	return local(t, 8*time.Minute, "helm", args...)
}

// TestF3NoCNIHelmPath: on a no-CNI cluster helm install is refused by the preflight;
// `ichor bootstrap cni`, helm install, then adopt recognises Ichor (Helm) and Ichor's Cilium.
func TestF3NoCNIHelmPath(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	before := cniPaths(t, c)
	out, err := helmInstall(t, c, false)
	if err == nil || !strings.Contains(out, "this cluster has no CNI") || !strings.Contains(out, "ichor bootstrap cni") {
		t.Errorf("helm install was not refused by the preflight (err=%v)", err)
	}
	if got, err := inner(c, "get", "crd", "--no-headers"); err == nil && strings.Contains(got, "ichor.maesh.dev") {
		t.Errorf("Ichor CRDs exist after the refused helm install: %s", got)
	}
	start := time.Now()
	out, err = local(t, 9*time.Minute, repoPath(t, "bin/img/ichor/ichor"), "cni", "bootstrap", "--kube-proxy", "enabled", "--tunnel", "geneve", "--yes", "--kubeconfig", c.LocalKube)
	t.Logf("cni bootstrap took %s", time.Since(start))
	if err != nil {
		t.Fatalf("cni bootstrap failed: %v\n%s", err, out)
	}
	start = time.Now()
	if out, err = helmInstall(t, c, true); err != nil {
		t.Fatalf("helm install failed after the bootstrap: %v\n%s", err, out)
	}
	t.Logf("helm install took %s", time.Since(start))
	start = time.Now()
	out, err = adopt(t, c)
	t.Logf("adopt took %s", time.Since(start))
	if err != nil {
		t.Fatalf("adopt failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cilium (ichor)") {
		t.Errorf("adopt did not recognise Ichor's Cilium")
	}
	if strings.Contains(out, "installing Cilium") {
		t.Errorf("adopt installed Cilium again")
	}
	membership(t, c)
	sameHashes(t, before, cniPaths(t, c))
	holdCleared(t, c)
	t.Logf("spec.cni = %s", clusterJSONPath(c, "{.spec.cni}"))
	if got := clusterJSONPath(c, "{.spec.cni.tunnel}"); got != "geneve" { // the tunnel is read back from Ichor's Cilium
		t.Errorf("spec.cni.tunnel = %q, want geneve (the controller would flip the live tunnel with an ichor-cni Job)", got)
	}
	noCNIJob(t, c)
	if rel, _ := local(t, time.Minute, "helm", "list", "-A", "--kubeconfig", c.LocalKube); !strings.Contains(rel, "ichor") {
		t.Errorf("helm list does not show the ichor release")
	}
}

// TestF2FlannelHelm: on a flannel cluster, helm install then adopt --cni-unmanaged prints the
// policy advice and applies nothing into ichor-system.
func TestF2FlannelHelm(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	if out, err := helmInstall(t, c, true); err != nil {
		t.Fatalf("helm install failed: %v\n%s", err, out)
	}
	before := cniPaths(t, c)
	out, err := adopt(t, c, "--cni-unmanaged")
	if err != nil {
		t.Fatalf("adopt --cni-unmanaged failed: %v\n%s", err, out)
	}
	unmanagedAdopted(t, c)
	if !strings.Contains(strings.ToLower(out), "network polic") {
		t.Errorf("adopt printed no network-policy advice")
	}
	if pols := policiesIn(c); pols != "" {
		t.Errorf("policies were applied into %s: %s", crNS, pols)
	}
	if np, _ := inner(c, "-n", crNS, "get", "networkpolicies.networking.k8s.io", "--no-headers"); strings.TrimSpace(np) != "" {
		t.Logf("NetworkPolicies in %s (from the chart, if any):\n%s", crNS, np)
	}
	sameHashes(t, before, cniPaths(t, c))
}
