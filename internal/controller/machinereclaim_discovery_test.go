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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A reclaim that ends Done wakes the target cluster's discovery at once instead of leaving the new
// maintenance node to the next 5 minute pass (ADR 0107 amendment).
func TestDoneReclaimEnqueuesItsCluster(t *testing.T) {
	mr := &ichorv1alpha1.MachineReclaim{
		Namespace: "ichor-system", Name: "r",
		Spec:   ichorv1alpha1.MachineReclaimSpec{ClusterRef: &corev1.LocalObjectReference{Name: "c"}},
		Status: ichorv1alpha1.MachineReclaimStatus{Phase: ichorv1alpha1.MachineReclaimDone},
	}

	got := reclaimDoneClusters(context.Background(), mr)
	if len(got) != 1 || got[0] != (reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ichor-system", Name: "c"}}) {
		t.Fatalf("requests %v", got)
	}

	mr.Status.Phase = ichorv1alpha1.MachineReclaimWaitingForMaintenance
	if got := reclaimDoneClusters(context.Background(), mr); len(got) != 0 {
		t.Fatalf("a request still waiting enqueued %v", got)
	}

	mr.Status.Phase, mr.Spec.ClusterRef = ichorv1alpha1.MachineReclaimDone, nil
	if got := reclaimDoneClusters(context.Background(), mr); len(got) != 0 {
		t.Fatalf("a request without a cluster enqueued %v", got)
	}
}
