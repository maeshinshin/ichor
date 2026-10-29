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

package controller

import (
	"context"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

func unmanagedCluster() *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{}
	c.Spec.CNI.Preset = render.PresetUnmanaged

	return c
}

// TestUnmanagedClusterNeverBlocksAKubernetesUpgrade: there is no hand-over to wait for (ADR 0103).
func TestUnmanagedClusterNeverBlocksAKubernetesUpgrade(t *testing.T) {
	t.Parallel()

	if msg := cniHandoverPending(unmanagedCluster()); msg != "" {
		t.Errorf("cniHandoverPending = %q, want empty", msg)
	}

	// A cilium cluster with no hand-over yet still blocks.
	c := &ichorv1alpha1.TalosCluster{}
	c.Spec.CNI.Preset = ciliumName

	if cniHandoverPending(c) == "" {
		t.Error("a cilium cluster without CNIHandover must still block")
	}
}

// TestCNIExecutorRefusesAnUnmanagedCluster covers the chart upgrade and the kube-proxy switch: the
// guard runs before anything is read, so no client is needed.
func TestCNIExecutorRefusesAnUnmanagedCluster(t *testing.T) {
	t.Parallel()

	for _, op := range []*ichorv1alpha1.TalosOperation{
		{Spec: ichorv1alpha1.TalosOperationSpec{
			Kind: ichorv1alpha1.TalosOperationKindCNI,
			CNI:  &ichorv1alpha1.TalosOperationCNISpec{Action: ichorv1alpha1.TalosOperationCNIActionUpgrade, ChartVersion: "1.0.0"},
		}},
		{Spec: ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindCNI}},
	} {
		done, err := (&cniKubeProxyAdvancer{}).Advance(context.Background(), unmanagedCluster(), &ichorv1alpha1.TalosMachine{}, op)
		if done || err == nil || err.Error() != ichorv1alpha1.CNINotManagedMessage {
			t.Errorf("Advance(%+v) = %v, %v; want the not-managed refusal", op.Spec.CNI, done, err)
		}
	}
}

// TestUnmanagedIsNotCNIUnsupported: the machine controller's refusal is for the removed presets
// only (flannel, custom), and the unmanaged preset passes it.
func TestUnmanagedIsNotCNIUnsupported(t *testing.T) {
	t.Parallel()

	r := &TalosMachineReconciler{}

	if _, stop, err := r.refuseUnsupportedCNI(context.Background(), unmanagedCluster(), &ichorv1alpha1.TalosMachine{}); stop || err != nil {
		t.Errorf("unmanaged: stop=%v err=%v, want neither", stop, err)
	}
}
