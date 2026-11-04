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
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// ADR 0128 3: --cilium-values is also the operator's values for a Cilium that is taken over and has
// no Helm release record (an inline manifest made with `helm template`).

// takeoverFlow is a Cilium of the Talos guide (no release record) on a cluster Ichor has never seen.
type takeoverFlow struct {
	*noCNIFlow

	mu     sync.Mutex
	inputs []cilium.PreflightInput
	// foreign, when set, is what the preflight reports as the Cilium's release records.
	foreign *cilium.Foreign
}

func newTakeoverFlow(t *testing.T) *takeoverFlow {
	t.Helper()

	f := &takeoverFlow{noCNIFlow: newNoCNIFlow(t)}

	ready := func(name, addr, cidr string) *corev1.Node {
		n := cniNode(name, cidr, true, "")
		n.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: addr}}

		return n
	}

	f.deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
		ready("cp", flowCPAddr, "10.244.0.0/24"), ready("wk", flowWKAddr, nodeCIDR),
		testDS("cilium", ciliumImage), ciliumCM("false"),
	).Build()
	f.deps.Preflight = func(_ context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.inputs = append(f.inputs, in)

		rep := &cilium.PreflightReport{Foreign: f.foreign}
		if f.foreign == nil {
			rep.UserValues = cilium.UserValues(in.UserValues)
		}

		return rep, nil
	}

	return f
}

func (f *takeoverFlow) preflights() []cilium.PreflightInput {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]cilium.PreflightInput(nil), f.inputs...)
}

// The operator's values reach the preflight (so the diff gate compares the live objects with a render
// that includes them) and the TalosCluster adopt writes (so the controller keeps them).
func TestAdoptTakeoverCarriesCiliumValues(t *testing.T) {
	f := newTakeoverFlow(t)
	f.deps.CiliumValues = debugValues()

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	pf := f.preflights()
	if len(pf) == 0 {
		t.Fatal("the takeover preflight did not run")
	}

	last := pf[len(pf)-1]

	if _, ok := last.UserValues[keyDebugOn]; !ok {
		t.Errorf("the preflight did not get the operator's values: %v", last.UserValues)
	}

	if dbg, _ := last.Effective[keyDebugOn].(map[string]any); dbg[keyDebugEnabled] != true {
		t.Errorf("the effective values the diff gate renders do not include them: %v", last.Effective[keyDebugOn])
	}

	var written ichorv1alpha1.TalosCluster
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: f.env.cluster.Name}, &written); err != nil {
		t.Fatal(err)
	}

	if written.Spec.CNI.Cilium == nil || written.Spec.CNI.Cilium.Values == nil {
		t.Fatalf("the TalosCluster carries no Cilium values: %+v", written.Spec.CNI)
	}

	var got map[string]any
	if err := json.Unmarshal(written.Spec.CNI.Cilium.Values.Raw, &got); err != nil {
		t.Fatal(err)
	}

	if dbg, _ := got[keyDebugOn].(map[string]any); dbg[keyDebugEnabled] != true {
		t.Errorf("written values = %v, want the operator's", got)
	}
}

// An Ichor-owned key is refused with nothing written, as in the install path.
func TestAdoptTakeoverRefusesAnOwnedCiliumValue(t *testing.T) {
	f := newTakeoverFlow(t)
	f.deps.CiliumValues = map[string]any{"kubeProxyReplacement": true}

	err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "kubeProxyReplacement") {
		t.Fatalf("runAdopt = %v, want the owned-key refusal", err)
	}

	if len(f.events()) != 0 {
		t.Fatalf("wrote despite the refusal: %v", f.events())
	}
}

// A Cilium with a Helm release record has its values in the record: two sources would disagree
// silently, so the flag is refused and nothing is written.
func TestAdoptTakeoverRefusesCiliumValuesWhenARecordExists(t *testing.T) {
	f := newTakeoverFlow(t)
	f.foreign = &cilium.Foreign{Namespace: "kube-system"}
	f.deps.CiliumValues = debugValues()

	err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "--cilium-values") || !strings.Contains(err.Error(), "release record") {
		t.Fatalf("runAdopt = %v, want the record refusal", err)
	}

	if len(f.events()) != 0 {
		t.Fatalf("wrote despite the refusal: %v", f.events())
	}
}

// Without the flag nothing changes: no values are invented.
func TestAdoptTakeoverWithoutCiliumValuesWritesNone(t *testing.T) {
	f := newTakeoverFlow(t)

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	var written ichorv1alpha1.TalosCluster
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: f.env.cluster.Name}, &written); err != nil {
		t.Fatal(err)
	}

	if written.Spec.CNI.Cilium != nil {
		t.Fatalf("values were written without --cilium-values: %+v", written.Spec.CNI.Cilium)
	}
}

// An unmanaged adoption installs and takes over nothing, so the values have nowhere to go.
func TestAdoptUnmanagedRefusesCiliumValues(t *testing.T) {
	f := newTakeoverFlow(t)
	f.deps.CNIUnmanaged = true
	f.deps.CiliumValues = debugValues()

	err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "--cilium-values only applies") {
		t.Fatalf("runAdopt = %v, want the refusal", err)
	}
}
