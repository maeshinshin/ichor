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
	"encoding/json"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

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
	check := overrideCheckFor(bgUnitHandler(t).Client)

	return &TalosClusterCustomValidator{APIReader: reader, Override: check},
		&TalosMachineCustomValidator{APIReader: reader, Override: check}, worker
}

func admissionCtxAs(user string) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		UserInfo: authnv1.UserInfo{Username: user},
	})
}

func TestLastWorkerGuardMachineDelete(t *testing.T) {
	t.Parallel()

	_, v, worker := lastWorkerFixture(t)

	_, err := v.ValidateDelete(admissionCtxAs(bgUnitPlain), worker)
	if err == nil || !strings.Contains(err.Error(), "override") {
		t.Fatalf("a plain user deleting the last worker should be refused naming override, got %v", err)
	}

	if _, err := v.ValidateDelete(admissionCtxAs(bgUnitHolder), worker); err != nil {
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

	if _, err := v.ValidateUpdate(admissionCtxAs(bgUnitPlain), worker, rejected); err == nil {
		t.Fatal("a plain user un-approving the last worker should be refused")
	}

	if _, err := v.ValidateUpdate(admissionCtxAs(bgUnitHolder), worker, rejected); err != nil {
		t.Fatalf("an override holder should be allowed, got %v", err)
	}
}

// A node operation that carries overrideReason (the controller-side override of the last-worker
// guard) is admitted only for a user holding the verb on the machine.
func TestLastWorkerGuardNodeOperationReason(t *testing.T) {
	t.Parallel()

	h := bgUnitHandler(t)

	handle := func(user, reason string) admission.Response {
		op := ichorv1alpha1.TalosOperation{
			Name: "op", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: lwCluster}, Kind: ichorv1alpha1.TalosOperationKindNode,
				Node: &ichorv1alpha1.TalosOperationNodeSpec{
					MachineRef: corev1.LocalObjectReference{Name: "w1"}, Action: ichorv1alpha1.NodeActionReboot,
					OverrideReason: reason,
				},
			},
		}

		raw, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}

		return h.Handle(context.Background(), admission.Request{
			Operation: admissionv1.Create, Name: "op", Namespace: testNamespace,
			Resource: metav1.GroupVersionResource{Group: ichorGroup, Version: "v1alpha1", Resource: resOperations},
			UserInfo: authnv1.UserInfo{Username: user}, Object: runtime.RawExtension{Raw: raw},
		})
	}

	if !handle(bgUnitPlain, "").Allowed {
		t.Error("a plain reboot needs no override")
	}

	if handle(bgUnitPlain, "teardown").Allowed {
		t.Error("a plain user must not set overrideReason")
	}

	if !handle(bgUnitHolder, "teardown").Allowed {
		t.Error("an override holder may set overrideReason")
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

	_, err := v.ValidateUpdate(admissionCtxAs(bgUnitPlain), oldC, newC)
	if err == nil || !strings.Contains(err.Error(), "override") {
		t.Fatalf("a plain user should be refused naming override, got %v", err)
	}

	if _, err := v.ValidateUpdate(admissionCtxAs(bgUnitHolder), oldC, newC); err != nil {
		t.Fatalf("an override holder should be allowed, got %v", err)
	}
}
