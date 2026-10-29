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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// cniWipe removes Cilium (agents, envoy, operator) and the CNI configuration it left on every node, so the nodes go
// NotReady with "cni plugin not initialized": a cluster whose Cilium is gone after adopt's step 2. The wipe is a
// host-network DaemonSet (no pod network needed) running an image the nodes already have; it keeps removing the
// files while the agents terminate. The inner kubectl wrapper refuses delete, so Cilium is scaled away by patch.
const cniWipe = `apiVersion: apps/v1
kind: DaemonSet
metadata: {name: cni-wipe, namespace: kube-system}
spec:
  selector: {matchLabels: {app: cni-wipe}}
  template:
    metadata: {labels: {app: cni-wipe}}
    spec:
      hostNetwork: true
      tolerations: [{operator: Exists}]
      containers:
      - name: wipe
        image: quay.io/cilium/cilium:v1.18.0
        securityContext: {privileged: true}
        command: ["sh", "-c", "while true; do rm -f /host/*; sleep 2; done"]
        volumeMounts: [{name: cni, mountPath: /host}]
      volumes: [{name: cni, hostPath: {path: /etc/cni/net.d}}]
`

// TestE6NoPodNetwork (ADR 0104 E6, second half): Cilium is removed right after adopt's Cilium step; the Ichor pods
// cannot be scheduled on NotReady nodes and adopt must give up early naming the missing pod network.
func TestE6NoPodNetwork(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNINone})
	manifest := filepath.Join(t.TempDir(), "wipe.yaml")
	if err := os.WriteFile(manifest, []byte(cniWipe), 0o600); err != nil {
		t.Fatal(err)
	}
	wipe := func() {
		for _, a := range [][]string{
			{"-n", "kube-system", "patch", "ds/cilium", "-p", `{"spec":{"template":{"spec":{"nodeSelector":{"gone":"gone"}}}}}`},
			{"-n", "kube-system", "patch", "ds/cilium-envoy", "-p", `{"spec":{"template":{"spec":{"nodeSelector":{"gone":"gone"}}}}}`},
			{"-n", "kube-system", "scale", "deploy/cilium-operator", "--replicas=0"},
			{"apply", "-f", manifest},
		} {
			o, err := inner(c, a...)
			t.Logf("kubectl %s: %v\n%s", strings.Join(a, " "), err, o)
		}
	}
	out, took, ran, err := adoptTriggered(t, c, "/tmp/ichor", "Cilium is installed and the nodes are Ready", 0, wipe,
		append([]string{"--wait-timeout", "6m"}, geneve...)...)
	t.Logf("adopt ran %s, wipe ran=%v, err=%v", took, ran, err)
	nodes, _ := inner(c, "get", "nodes", "--no-headers")
	t.Logf("nodes:\n%s", nodes)
	pods, _ := inner(c, "-n", crNS, "get", "pods", "--no-headers")
	t.Logf("pods in %s:\n%s", crNS, pods)
	if err == nil {
		t.Fatalf("adopt succeeded without a pod network:\n%s", out)
	}
	if !strings.Contains(out, "has no pod network") {
		t.Errorf("adopt did not fail with the no-pod-network message")
	}
}
