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

package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const staticInterface = `    network:
        interfaces:
            - interface: enp1s0
              addresses:
                  - 10.5.0.2/24
              routes:
                  - network: 0.0.0.0/0
                    gateway: 10.5.0.1
              dhcp: false
`

// A node whose config carries the legacy machine.network.interfaces block was blocked, because
// spec.network renders the multi-document form (a different shape, not just different values). Adopt
// captures the interfaces subtree, and only it, into the machine's TalosConfigPatch, which renders the
// node byte for byte and so changes nothing on it.
func TestAdoptCapturesTheLegacyNetworkInterfacesIntoAPatch(t *testing.T) {
	cp, wk := classAFixture(t, "v114")
	cp = []byte(strings.Replace(string(cp), "    features:\n", staticInterface+"    features:\n", 1))

	env, deps := classAFlow(t, "v1.14.3", cp, wk)
	planOutput(t, env, deps, false)

	deps.PlanOnly, deps.AssumeYes = false, true
	if err := runAdopt(context.Background(), "d1", testNamespace, deps); err == nil || !strings.Contains(err.Error(), "did not become Ready") {
		t.Fatalf("runAdopt = %v, want the Ready timeout\n%s", err, env.stderr.String())
	}

	var patch ichorv1alpha1.TalosConfigPatch
	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "adopt-uuid-" + flowCPAddr}, &patch); err != nil {
		t.Fatal(err)
	}

	if len(patch.Spec.Documents) != 1 || patch.Spec.Documents[0].Config == nil {
		t.Fatalf("documents = %+v, want one Config document", patch.Spec.Documents)
	}

	c := patch.Spec.Documents[0].Config
	if c.ClusterConfig != nil || c.MachineConfig.MachineNetwork.NetworkHostname != "" {
		t.Errorf("the patch carries more than machine.network.interfaces: %+v", c)
	}

	if raw, _ := json.Marshal(c); strings.Contains(string(raw), "token") || strings.Contains(string(raw), "secretbox") {
		t.Errorf("the patch carries secrets: %s", raw)
	}

	devs := c.MachineConfig.MachineNetwork.NetworkInterfaces
	if len(devs) != 1 || devs[0].DeviceInterface != "enp1s0" || devs[0].DeviceAddresses[0] != "10.5.0.2/24" {
		t.Errorf("interfaces = %+v", devs)
	}

	// A rerun renders from the written patch: nothing is left to capture.
	deps.PlanOnly, deps.AssumeYes = true, false
	env.stderr.Reset()

	if again := planOutput(t, env, deps, false); strings.Count(again, "already matches a fresh render") != 2 {
		t.Errorf("the rerun plan does not match both machines:\n%s", again)
	}
}

// The mirror drops a WireGuard private key, so the captured block would not even validate: the
// adopt fails before writing anything instead of adopting the node with the key gone.
func TestAdoptRefusesInterfacesItCannotReproduce(t *testing.T) {
	cp, wk := classAFixture(t, "v114")
	wg := staticInterface + "              wireguard:\n                  privateKey: " + strings.Repeat("A", 43) + "=\n                  listenPort: 51820\n"
	cp = []byte(strings.Replace(string(cp), "    features:\n", wg+"    features:\n", 1))

	env, deps := classAFlow(t, "v1.14.3", cp, wk)
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), "d1", testNamespace, deps); err == nil || !strings.Contains(err.Error(), "zero-drift patch") {
		t.Fatalf("runAdopt = %v, want the zero-drift verification to fail\n%s", err, env.stderr.String())
	}
}
