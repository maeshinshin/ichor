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
	// Services work: a ClusterIP reached from a pod (the kubernetes Service, then cluster DNS through kube-dns).
	probe, perr := inner(c, "run", "svc-probe", "--restart=Never", "--rm", "-i", "--image=quay.io/cilium/cilium:v1.18.0", "--command", "--",
		"sh", "-c", "curl -sk -m 10 -o /dev/null -w 'kubernetes=%{http_code}\\n' https://10.249.0.1:443/version; getent hosts kubernetes.default.svc.cluster.local")
	t.Logf("service probe: %v\n%s", perr, probe)
	if perr != nil || !strings.Contains(probe, "kubernetes=") || strings.Contains(probe, "kubernetes=000") {
		t.Errorf("a Service ClusterIP was not reachable from a pod")
	}
}
