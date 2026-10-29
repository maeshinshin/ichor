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

import (
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// policiesIn lists the CiliumNetworkPolicies in ichor-system; an unserved CRD (flannel, Calico) lists none.
func policiesIn(c *foreign.Cluster) string {
	out, err := inner(c, "-n", crNS, "get", "ciliumnetworkpolicies.cilium.io", "--no-headers")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// e7 adopts a foreign cluster with --cni-unmanaged and checks the network-policy gate: the notice
// and no policies off Cilium, "applied" and the policies on a Cilium that is not Ichor's.
func e7(t *testing.T, cni, detected string, wantPolicies bool) {
	c := build(t, foreign.Options{Workers: 1, CNI: cni})
	start := time.Now()
	out, err := adopt(t, c, "--cni-unmanaged")
	t.Logf("adopt took %s", time.Since(start))
	if err != nil {
		t.Fatalf("adopt --cni-unmanaged failed: %v\n%s", err, out)
	}
	unmanagedAdopted(t, c)
	waitCluster(t, c, "{.status.cni.detected}", detected)
	pols := policiesIn(c)
	t.Logf("CiliumNetworkPolicies in %s:\n%s", crNS, pols)
	notice := "not applying Ichor's network policies: the detected CNI is \"" + detected + "\", not cilium"
	if wantPolicies {
		if !strings.Contains(out, "applied Ichor's network policies") || pols == "" {
			t.Errorf("Ichor's policies were not applied on a Cilium adopted --cni-unmanaged")
		}
		return
	}
	if !strings.Contains(out, notice) {
		t.Errorf("adopt did not print %q", notice)
	}
	if pols != "" {
		t.Errorf("policies exist on a %s cluster: %s", detected, pols)
	}
}

// TestE7Flannel: flannel gets the notice and no policies.
func TestE7Flannel(t *testing.T) { e7(t, foreign.CNIFlannel, "flannel", false) }

// TestE7Calico: Calico gets the notice and no policies.
func TestE7Calico(t *testing.T) { e7(t, foreign.CNICalico, "calico", false) }

// TestE7CiliumUnmanaged: a Helm Cilium adopted --cni-unmanaged gets Ichor's policies.
func TestE7CiliumUnmanaged(t *testing.T) { e7(t, foreign.CNICiliumHelm, "cilium", true) }
