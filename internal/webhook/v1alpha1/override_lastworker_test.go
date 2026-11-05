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

package v1alpha1

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// lastWorkerFixture is an unschedulable cluster with exactly one approved worker, behind a validator
// whose override check asks the fake SubjectAccessReview of bgUnitHandler (only bgUnitHolder holds it).
func lastWorkerFixture(t *testing.T) (*TalosClusterCustomValidator, *TalosMachineCustomValidator, *ichorv1alpha1.TalosMachine) {
	t.Helper()

	scheme := schemeOf(t)

	cluster := &ichorv1alpha1.TalosCluster{Name: lwCluster, Namespace: testNamespace}
	cluster.Spec.ControlPlane.Schedulable = "false"

	worker := &ichorv1alpha1.TalosMachine{
		Name: "w1", Namespace: testNamespace,
		Annotations: map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue},
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: lwCluster}, Role: roleWorker,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, worker).Build()
	check := rightCheckFor(bgUnitHandler(t).Client)

	return &TalosClusterCustomValidator{APIReader: reader, Can: check},
		&TalosMachineCustomValidator{APIReader: reader, Can: check}, worker
}

func TestLastWorkerGuardMachineDelete(t *testing.T) {
	t.Parallel()

	_, v, worker := lastWorkerFixture(t)

	_, err := v.ValidateDelete(settingsCtx(bgUnitPlain, false), worker)
	if err == nil || !strings.Contains(err.Error(), "override") {
		t.Fatalf("a plain user deleting the last worker should be refused naming override, got %v", err)
	}

	if _, err := v.ValidateDelete(settingsCtx(bgUnitHolder, false), worker); err != nil {
		t.Fatalf("an override holder should be allowed to delete the last worker, got %v", err)
	}

	// No admission request in the context: no user, so no override.
	if _, err := v.ValidateDelete(context.Background(), worker); err == nil {
		t.Fatal("without an admission request the guard must still refuse")
	}
}

func TestLastWorkerGuardMachineUnapprove(t *testing.T) {
	t.Parallel()

	_, v, worker := lastWorkerFixture(t)
	rejected := worker.DeepCopy()
	rejected.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected

	if _, err := v.ValidateUpdate(settingsCtx(bgUnitPlain, false), worker, rejected); err == nil {
		t.Fatal("a plain user un-approving the last worker should be refused")
	}

	if _, err := v.ValidateUpdate(settingsCtx(bgUnitHolder, false), worker, rejected); err != nil {
		t.Fatalf("an override holder should be allowed, got %v", err)
	}
}

// A node operation that asks to override the controller's last-worker guard is admitted only for a user
// holding the verb on the machine. The request may be the override flag, a reason alone (how operations
// were written before the flag, ADR 0110), or the deprecated breakGlassReason; the gate is the same.
func TestLastWorkerGuardNodeOperationOverride(t *testing.T) {
	t.Parallel()

	h := bgUnitHandler(t)

	handle := func(user string, node ichorv1alpha1.TalosOperationNodeSpec) admission.Response {
		node.MachineRef = corev1.LocalObjectReference{Name: "w1"}
		node.Action = ichorv1alpha1.NodeActionReboot
		return h.Handle(context.Background(), createOperationRequest(t, ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: lwCluster}, Kind: ichorv1alpha1.TalosOperationKindNode, Node: &node,
		}, user))
	}

	const teardown = "teardown"

	asks := map[string]ichorv1alpha1.TalosOperationNodeSpec{
		"no override":        {},
		"override flag":      {Override: true},
		"override with note": {Override: true, OverrideReason: teardown},
		"reason alone":       {OverrideReason: teardown},
		"deprecated alias":   {BreakGlassReason: teardown},
	}

	if !handle(bgUnitPlain, asks["no override"]).Allowed {
		t.Error("a plain reboot needs no override")
	}

	for _, name := range []string{"override flag", "override with note", "reason alone", "deprecated alias"} {
		if handle(bgUnitPlain, asks[name]).Allowed {
			t.Errorf("a plain user must not override with %s", name)
		}

		if !handle(bgUnitHolder, asks[name]).Allowed {
			t.Errorf("an override holder may override with %s", name)
		}
	}
}

func TestLastWorkerGuardClusterUnschedulable(t *testing.T) {
	t.Parallel()

	// A cluster that is schedulable and has no approved worker: turning it unschedulable is refused.
	v, _, worker := lastWorkerFixture(t)
	if err := v.APIReader.(client.Client).Delete(context.Background(), worker); err != nil {
		t.Fatal(err)
	}

	oldC := &ichorv1alpha1.TalosCluster{Name: lwCluster, Namespace: testNamespace}
	oldC.Spec.ControlPlane.Schedulable = "true"
	newC := oldC.DeepCopy()
	newC.Spec.ControlPlane.Schedulable = "false"

	_, err := v.ValidateUpdate(settingsCtx(bgUnitPlain, false), oldC, newC)
	if err == nil || !strings.Contains(err.Error(), "override") {
		t.Fatalf("a plain user should be refused naming override, got %v", err)
	}

	if _, err := v.ValidateUpdate(settingsCtx(bgUnitHolder, false), oldC, newC); err != nil {
		t.Fatalf("an override holder should be allowed, got %v", err)
	}
}
