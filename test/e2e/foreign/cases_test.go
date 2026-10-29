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

// Hardware cases (groups E and F): a cluster with no CNI gets Ichor's Cilium before Ichor.

import (
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// noCNINothingWritten asserts a refused adopt left the cluster and the machine configs as they were.
func noCNINothingWritten(t *testing.T, c *foreign.Cluster, before map[string]string) {
	t.Helper()
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
	if got, _ := inner(c, "get", "ds", "-A", "--no-headers"); strings.Contains(got, "cilium") {
		t.Errorf("a Cilium DaemonSet exists after a refusal: %s", got)
	}
}

// managedAdopted asserts what every no-CNI managed adoption shares (E1, E2).
func managedAdopted(t *testing.T, c *foreign.Cluster, before map[string]string) {
	t.Helper()
	membership(t, c)
	if rec, _ := inner(c, "-n", crNS, "get", "secrets", "-l", "owner=helm,name=cilium", "--no-headers"); strings.TrimSpace(rec) == "" {
		t.Errorf("no Cilium release record in %s", crNS)
	}
	if got := clusterJSONPath(c, "{.spec.cni.preset}"); got != "cilium" {
		t.Errorf("spec.cni.preset = %q, want cilium", got)
	}
	waitCluster(t, c, "{.status.conditions[?(@.type==\"CNIHandover\")].status}", "True")
	noCNIJob(t, c)
	holdCleared(t, c)
	pods, _ := inner(c, "-n", crNS, "get", "pods", "--no-headers")
	t.Logf("pods in %s:\n%s", crNS, pods)
	for _, l := range strings.Split(strings.TrimSpace(pods), "\n") {
		if l != "" && !strings.Contains(l, "Running") && !strings.Contains(l, "Completed") {
			t.Errorf("pod not Running: %s", l)
		}
	}
	nodes, _ := inner(c, "get", "nodes", "--no-headers")
	t.Logf("nodes:\n%s", nodes)
	if strings.Contains(nodes, "NotReady") {
		t.Errorf("nodes not Ready: %s", nodes)
	}
	time.Sleep(3 * time.Minute) // the controller renders and applies; it must change no CNI-relevant path
	sameHashes(t, before, cniPaths(t, c))
	out, err := ichorCLI(t, c, "cluster", "status", c.Opts.Prefix)
	if err != nil || !strings.Contains(out, "cni: cilium\n") || strings.Contains(out, "unmanaged") {
		t.Errorf("cluster status does not say managed Cilium (err=%v)", err)
	}
}

// TestE1 (replaces D5): a no-CNI cluster is adopted managed; Cilium goes in first.
func TestE1NoCNIAdoptedManaged(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	before := cniPaths(t, c)
	start := time.Now()
	out, err := adopt(t, c, geneve...)
	t.Logf("adopt took %s", time.Since(start))
	if err != nil {
		t.Fatalf("adopt failed: %v\n%s", err, out)
	}
	managedAdopted(t, c, before)
}

// TestE3: --cni-unmanaged on a no-CNI cluster is refused and nothing is written.
func TestE3NoCNIUnmanagedRefused(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	before := cniPaths(t, c)
	out, err := adopt(t, c, "--cni-unmanaged")
	if err == nil {
		t.Fatalf("adopt --cni-unmanaged succeeded on a no-CNI cluster:\n%s", out)
	}
	noCNINothingWritten(t, c, before)
}

// TestE4: a no-CNI cluster with KubePrism disabled is refused with the enable-KubePrism message.
func TestE4NoKubePrismRefused(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone, NoKubePrism: true})
	before := cniPaths(t, c)
	out, err := adopt(t, c)
	if err == nil {
		t.Fatalf("adopt succeeded with KubePrism disabled:\n%s", out)
	}
	if !strings.Contains(out, "Enable KubePrism") {
		t.Errorf("refusal does not carry the enable-KubePrism message: %s", out)
	}
	noCNINothingWritten(t, c, before)
}

// geneve: the outer cluster's own Cilium uses VXLAN on UDP 8472, which swallows a nested VXLAN tunnel (see
// foreign.ciliumHelmArgs), so every Cilium Ichor installs here uses Geneve.
var geneve = []string{"--tunnel", "geneve"}
