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
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// `ichor bootstrap cni`: the Helm path's step before helm install ichor.

const errNotNoCNI = "not a cluster with no CNI"

type bootstrapRun struct {
	deps   cniBootstrapDeps
	stderr *bytes.Buffer
	calls  []bootstrapCall
}

func newBootstrapRun(t *testing.T, objs ...client.Object) *bootstrapRun {
	t.Helper()

	r := &bootstrapRun{stderr: &bytes.Buffer{}}
	r.deps = cniBootstrapDeps{
		Target:    fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build(),
		KubeProxy: kubeProxyReplaced,
		Stdin:     strings.NewReader("y\n"),
		Stderr:    r.stderr,
		Install: func(_ context.Context, c *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) error {
			r.calls = append(r.calls, bootstrapCall{cluster: c.DeepCopy(), mode: mode})

			return nil
		},
	}

	return r
}

func noCNINodes() []client.Object {
	return []client.Object{cniNode("cp", "10.244.0.0/24", false, netNotReady), cniNode("wk", nodeCIDR, false, netNotReady)}
}

func TestCNIBootstrap_InstallsWhatAdoptWouldInstall(t *testing.T) {
	r := newBootstrapRun(t, noCNINodes()...)
	r.deps.Tunnel = tunnelGeneve
	r.deps.Values = map[string]any{"rollOutCiliumPods": true}

	if err := runCNIBootstrap(context.Background(), r.deps); err != nil {
		t.Fatalf("%v\n%s", err, r.stderr)
	}

	if len(r.calls) != 1 || r.calls[0].mode != cni.KubeProxyReplaced {
		t.Fatalf("calls = %+v, want one in replaced mode", r.calls)
	}

	// The same effective values as the TalosCluster adopt writes for a fresh cluster.
	adopt := &ichorv1alpha1.TalosCluster{}
	adopt.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: cniCilium, KubeProxy: kubeProxyReplaced, Tunnel: tunnelGeneve}

	if err := setCiliumUserValues(adopt, r.deps.Values, kubeProxyReplaced, tunnelGeneve); err != nil {
		t.Fatal(err)
	}

	got, err := cilium.EffectiveValuesForMode(r.calls[0].cluster, cni.KubeProxyReplaced)
	if err != nil {
		t.Fatal(err)
	}

	want, err := cilium.EffectiveValuesForMode(adopt, cni.KubeProxyReplaced)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("effective values differ from adopt's:\n%v\n%v", got, want)
	}

	if !strings.Contains(r.stderr.String(), "Proceed? [y/N]") {
		t.Errorf("no consent prompt:\n%s", r.stderr)
	}
}

func TestCNIBootstrap_ConsentDeclinedInstallsNothing(t *testing.T) {
	r := newBootstrapRun(t, noCNINodes()...)
	r.deps.Stdin = strings.NewReader("n\n")

	if err := runCNIBootstrap(context.Background(), r.deps); err == nil || len(r.calls) != 0 {
		t.Fatalf("err = %v, calls = %d; want an abort and no install", err, len(r.calls))
	}
}

func TestCNIBootstrap_Refusals(t *testing.T) {
	ready := cniNode("cp", nodeCIDR, true, "")

	tests := []struct {
		name  string
		objs  []client.Object
		tweak func(*cniBootstrapDeps)
		want  string
	}{
		{"a Ready node", []client.Object{ready, cniNode("wk", nodeCIDR, false, netNotReady)}, nil, errNotNoCNI},
		{"flannel runs", append(noCNINodes(), testDS("kube-flannel", flannelImage)), nil, errNotNoCNI},
		{"a foreign Cilium", append(noCNINodes(), testDS("cilium", ciliumImage), helmRecord("kube-system")), nil, errNotNoCNI},
		{"no pod CIDR", []client.Object{cniNode("cp", "", false, netNotReady)}, nil, "spec.podCIDR"},
		{"no nodes", nil, nil, errNotNoCNI},
		{"kube-proxy mode", noCNINodes(), func(d *cniBootstrapDeps) { d.KubeProxy = "yes" }, "--kube-proxy"},
		{"tunnel", noCNINodes(), func(d *cniBootstrapDeps) { d.Tunnel = "gre" }, "--tunnel"},
		{"owned key", noCNINodes(), func(d *cniBootstrapDeps) { d.Values = map[string]any{"ipam": map[string]any{"mode": "x"}} }, "ipam.mode"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newBootstrapRun(t, tc.objs...)
			if tc.tweak != nil {
				tc.tweak(&r.deps)
			}

			err := runCNIBootstrap(context.Background(), r.deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}

			if len(r.calls) != 0 {
				t.Errorf("Cilium was installed despite the refusal")
			}
		})
	}
}

// A rerun after a wait that timed out: Ichor's own Cilium is there, nodes still NotReady.
func TestCNIBootstrap_RerunWithIchorsCiliumUpgradesIt(t *testing.T) {
	r := newBootstrapRun(t, append(noCNINodes(), testDS("cilium", ciliumImage), helmRecord("ichor-system"))...)

	if err := runCNIBootstrap(context.Background(), r.deps); err != nil || len(r.calls) != 1 {
		t.Fatalf("err = %v, calls = %d; want one install", err, len(r.calls))
	}
}

// Afterwards adopt must see Ichor's own Cilium and leave it alone: no takeover, no record deleted.
func TestAdopt_AfterBootstrapCiliumIsIchorsAndNotTakenOver(t *testing.T) {
	f := newNoCNIFlow(t)
	ctx := context.Background()

	node := func(name, cidr string) *corev1.Node { return cniNode(name, cidr, true, "") }
	record := helmRecord("ichor-system")
	f.deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
		node("cp", "10.244.0.0/24"), node("wk", nodeCIDR), testDS("cilium", ciliumImage), record, ciliumCM("false"),
	).Build()

	facts, err := gatherK8sFacts(ctx, f.deps.Target)
	if err != nil {
		t.Fatal(err)
	}

	rep := detectCNI(facts, CNIFacts{Kind: CNINone})
	if rep.NoCNI || rep.Provenance != provenanceIchor || (&adoptState{rep: rep}).needsTakeover() {
		t.Fatalf("report = %+v, want Ichor-owned Cilium and no takeover", rep)
	}

	if err := f.run(t); err != nil {
		t.Fatalf("adopt: %v\n%s", err, f.env.stderr.String())
	}

	if out := f.env.stderr.String(); len(f.calls()) != 0 || strings.Contains(out, "takeover") {
		t.Errorf("adopt installed or took over Cilium:\n%s", out)
	}

	if err := f.deps.Target.Get(ctx, client.ObjectKeyFromObject(record), &corev1.Secret{}); err != nil {
		t.Errorf("Ichor's release record was touched: %v", err)
	}
}
