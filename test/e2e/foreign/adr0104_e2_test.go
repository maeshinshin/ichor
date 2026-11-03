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

// TestE2 (ADR 0104 E2): the same with kube-proxy disabled in Talos; Cilium replaces it and Services work.
func TestE2NoCNIKubeProxyDisabled(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone, NoKubeProxy: true})
	before := cniPaths(t, c)
	start := time.Now()
	out, err := adopt(t, c, geneve...)
	t.Logf("adopt took %s", time.Since(start))
	if err != nil {
		t.Fatalf("adopt failed: %v\n%s", err, out)
	}
	if got := clusterJSONPath(c, "{.spec.cni.kubeProxy}"); got != "replaced" {
		t.Errorf("spec.cni.kubeProxy = %q, want replaced", got)
	}
	cfg, _ := inner(c, "-n", "kube-system", "get", "cm", "cilium-config", "-o", "jsonpath={.data.kube-proxy-replacement}")
	if strings.TrimSpace(cfg) != "true" {
		t.Errorf("cilium-config kube-proxy-replacement = %q, want true", cfg)
	}
	managedAdopted(t, c, before)
	// Services work with no kube-proxy: a pod resolves a name through the kube-dns ClusterIP, and the agent says it replaces kube-proxy.
	probe, perr := inner(c, "run", "svc-probe", "--restart=Never", "--rm", "-i", "--image=quay.io/cilium/cilium:v1.19.10", "--command", "--",
		"getent", "hosts", "kubernetes.default.svc.cluster.local")
	t.Logf("service probe: %v\n%s", perr, probe)
	if perr != nil || !strings.Contains(probe, "10.249.0.1") {
		t.Errorf("a pod could not resolve a name through the kube-dns Service")
	}
	st, _ := inner(c, "-n", "kube-system", "exec", "ds/cilium", "--", "cilium-dbg", "status")
	if !strings.Contains(st, "KubeProxyReplacement:    True") {
		t.Errorf("cilium status does not report KubeProxyReplacement True:\n%s", st)
	}
}
