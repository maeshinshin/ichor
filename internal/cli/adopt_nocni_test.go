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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// The no-CNI path of ADR 0104 (unit 3): detection, preconditions, refusals and plan text.

const nodeCIDR = "10.244.1.0/24"

const netNotReady = "container runtime network not ready: NetworkReady=false reason:NetworkPluginNotReady message:Network plugin returns error: cni plugin not initialized"

func cniNode(name, podCIDR string, ready bool, msg string) *corev1.Node {
	status, reason := corev1.ConditionTrue, "KubeletReady"
	if !ready {
		status, reason = corev1.ConditionFalse, "KubeletNotReady"
	}

	return &corev1.Node{
		Name: name,
		Spec: corev1.NodeSpec{PodCIDR: podCIDR},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: status, Reason: reason, Message: msg},
		}},
	}
}

// ADR 0104 1.1: no CNI needs all three signs; every other combination is not "no CNI".
func TestDetectCNI_NoCNIRequiresAllThree(t *testing.T) {
	notReady := func(n string) client.Object { return cniNode(n, "10.244.0.0/24", false, netNotReady) }
	otherMsg := cniNode("c", "10.244.2.0/24", false, "kubelet is posting ready status: PLEG is not healthy")
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(crdGVK)
	crd.SetName("ciliumnodes.cilium.io")

	tests := []struct {
		name  string
		talos CNIFacts
		objs  []client.Object
		want  bool
	}{
		{"all three", CNIFacts{Kind: CNINone}, []client.Object{notReady("a"), notReady("b")}, true},
		{"talos does not say none", CNIFacts{Kind: CNIFlannel}, []client.Object{notReady("a")}, false},
		{"a CNI workload exists", CNIFacts{Kind: CNINone}, []client.Object{notReady("a"), testDS("calico-node", calicoImage)}, false},
		{"CiliumNode CRD exists", CNIFacts{Kind: CNINone}, []client.Object{notReady("a"), crd}, false},
		{"one node is Ready", CNIFacts{Kind: CNINone}, []client.Object{notReady("a"), cniNode("b", nodeCIDR, true, "")}, false},
		{"NotReady for another reason", CNIFacts{Kind: CNINone}, []client.Object{notReady("a"), otherMsg}, false},
		{"no nodes read", CNIFacts{Kind: CNINone}, nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectCNI(factsFor(t, tc.objs...), tc.talos).NoCNI; got != tc.want {
				t.Fatalf("NoCNI = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckCNIAdoption_NoCNI(t *testing.T) {
	rep := detectCNI(factsFor(t, cniNode("a", "10.244.0.0/24", false, netNotReady)), CNIFacts{Kind: CNINone})

	if err := checkCNIAdoption(rep, false); err != nil {
		t.Errorf("managed: %v", err)
	}

	err := checkCNIAdoption(rep, true)
	if err == nil || !strings.Contains(err.Error(), "no pod can run on it, including Ichor's") || !strings.Contains(err.Error(), "without --cni-unmanaged") {
		t.Errorf("--cni-unmanaged: err = %v, want the ADR 0104 refusal", err)
	}
}

func TestCheckNoCNIPreconditions(t *testing.T) {
	ok := map[string]KubePrismFacts{"a": {Enabled: true, Port: 7445}, "b": {Enabled: true, Port: 7445}}
	withCIDR := detectCNI(factsFor(t, cniNode("a", "10.244.0.0/24", false, netNotReady)), CNIFacts{Kind: CNINone})
	noCIDR := detectCNI(factsFor(t, cniNode("a", "10.244.0.0/24", false, netNotReady), cniNode("b", "", false, netNotReady)), CNIFacts{Kind: CNINone})

	if err := checkNoCNIPreconditions(withCIDR, ok, kubePrismDocContract); err != nil {
		t.Errorf("good cluster: %v", err)
	}

	for name, kp := range map[string]map[string]KubePrismFacts{
		"disabled":   {"a": {Enabled: true, Port: 7445}, "b": {}},
		"other port": {"a": {Enabled: true, Port: 7446}},
	} {
		err := checkNoCNIPreconditions(withCIDR, kp, kubePrismDocContract)
		if err == nil || !strings.Contains(err.Error(), "Enable KubePrism") {
			t.Errorf("%s: err = %v, want the enable-KubePrism message", name, err)
		}
	}

	off := map[string]KubePrismFacts{"a": {}}

	for contract, want := range map[string]string{
		kubePrismDocContract: kubePrismDoc, "v1.14.2": kubePrismDoc, "": kubePrismDoc,
		"v1.13": "machine.features.kubePrism", "v1.12": "machine.features.kubePrism",
	} {
		err := checkNoCNIPreconditions(withCIDR, off, contract)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("contract %q: err = %v, want it to name %s", contract, err, want)
		}
	}

	err := checkNoCNIPreconditions(noCIDR, ok, kubePrismDocContract)
	if err == nil || !strings.Contains(err.Error(), "spec.podCIDR") || !strings.Contains(err.Error(), "b") {
		t.Errorf("no pod CIDR: err = %v, want the podCIDR message naming b", err)
	}
}

// noCNIDeps is a flow env whose target cluster has no CNI: two NotReady nodes and nothing else.
func noCNIDeps(t *testing.T, podCIDR string) (*flowEnv, adoptDeps) {
	t.Helper()

	env := newFlowEnv(t, cniCilium, nil)
	deps := env.deps(t)
	deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
		cniNode("n1", "10.244.0.0/24", false, netNotReady), cniNode("n2", podCIDR, false, netNotReady)).Build()
	deps.AssumeYes = true

	return env, deps
}

func TestAdoptNoCNI_PlanAnnouncesCiliumInstall(t *testing.T) {
	env, deps := noCNIDeps(t, nodeCIDR)
	deps.PlanOnly = true
	deps.Tunnel = tunnelGeneve

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	for _, want := range []string{"Cilium will be installed by adopt", "tunnel geneve", "kube-proxy enabled", "managed cilium preset"} {
		if !strings.Contains(env.stderr.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, env.stderr.String())
		}
	}

	if len(*env.writes) != 0 {
		t.Fatalf("--plan wrote: %v", *env.writes)
	}
}

func TestAdoptNoCNI_Refusals(t *testing.T) {
	tests := []struct {
		name   string
		cidr   string
		mutate func(*adoptDeps, *flowEnv)
		want   string
	}{
		{"--cni-unmanaged", nodeCIDR, func(d *adoptDeps, _ *flowEnv) { d.CNIUnmanaged = true }, "no pod can run on it, including Ichor's"},
		{"no pod CIDR", "", func(*adoptDeps, *flowEnv) {}, "spec.podCIDR"},
		{"KubePrism off", nodeCIDR, func(_ *adoptDeps, e *flowEnv) {
			for addr, cfg := range e.live {
				e.live[addr] = dropDocument(cfg, "KubePrismConfig")
			}
		}, "Enable KubePrism"},
		{"bad tunnel", nodeCIDR, func(d *adoptDeps, _ *flowEnv) { d.Tunnel = "wireguard" }, "--tunnel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, deps := noCNIDeps(t, tc.cidr)
			tc.mutate(&deps, env)

			err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runAdopt = %v, want %q", err, tc.want)
			}

			if len(*env.writes) != 0 {
				t.Fatalf("wrote despite the refusal: %v", *env.writes)
			}
		})
	}
}

// --tunnel and --cilium-values mean nothing when adopt installs no Cilium: the cluster already runs one,
// and an existing TalosCluster has no spec adopt creates to put the values in (ADR 0128 3 lets a takeover
// carry them only into a cluster adopt creates).
func TestAdoptCiliumFlagsRefusedWhenCiliumIsNotInstalled(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*adoptDeps)
		want   string
	}{
		{"tunnel flag", func(d *adoptDeps) { d.Tunnel = ciliumTunnelVxlan }, "--tunnel only applies"},
		{"cilium-values flag", func(d *adoptDeps) { d.CiliumValues = debugValues() }, "--cilium-values applies to the TalosCluster adopt creates"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFlowEnv(t, cniCilium, nil)
			deps := env.deps(t)
			deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(testDS("cilium", ciliumImage), ciliumCM("false")).Build()
			tc.mutate(&deps)

			if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runAdopt = %v, want %q", err, tc.want)
			}
		})
	}
}
