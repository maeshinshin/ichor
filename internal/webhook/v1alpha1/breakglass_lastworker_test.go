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
// whose break-glass check asks the fake SubjectAccessReview of bgUnitHandler (only bgUnitHolder holds it).
func lastWorkerFixture(t *testing.T) (*TalosClusterCustomValidator, *TalosMachineCustomValidator, *ichorv1alpha1.TalosMachine) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cluster := &ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Name: "west", Namespace: "default"}}
	cluster.Spec.ControlPlane.Schedulable = "false"

	worker := &ichorv1alpha1.TalosMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "w1", Namespace: "default",
			Annotations: map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue},
		},
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "west"}, Role: roleWorker,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}

	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, worker).Build()
	check := breakGlassCheckFor(bgUnitHandler(t, nil).Client)

	return &TalosClusterCustomValidator{APIReader: reader, BreakGlass: check},
		&TalosMachineCustomValidator{APIReader: reader, BreakGlass: check}, worker
}

func admissionCtxAs(user string) context.Context {
	return admission.NewContextWithRequest(context.Background(), admission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{UserInfo: authnv1.UserInfo{Username: user}},
	})
}

func TestLastWorkerGuardMachineDelete(t *testing.T) {
	t.Parallel()

	_, v, worker := lastWorkerFixture(t)

	_, err := v.ValidateDelete(admissionCtxAs(bgUnitPlain), worker)
	if err == nil || !strings.Contains(err.Error(), "break-glass") {
		t.Fatalf("a plain user deleting the last worker should be refused naming break-glass, got %v", err)
	}

	if _, err := v.ValidateDelete(admissionCtxAs(bgUnitHolder), worker); err != nil {
		t.Fatalf("a break-glass holder should be allowed to delete the last worker, got %v", err)
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
		t.Fatalf("a break-glass holder should be allowed, got %v", err)
	}
}

func TestLastWorkerGuardClusterUnschedulable(t *testing.T) {
	t.Parallel()

	// A cluster that is schedulable and has no approved worker: turning it unschedulable is refused.
	v, _, worker := lastWorkerFixture(t)
	if err := v.APIReader.(client.Client).Delete(context.Background(), worker); err != nil {
		t.Fatal(err)
	}

	oldC := &ichorv1alpha1.TalosCluster{ObjectMeta: metav1.ObjectMeta{Name: "west", Namespace: "default"}}
	oldC.Spec.ControlPlane.Schedulable = "true"
	newC := oldC.DeepCopy()
	newC.Spec.ControlPlane.Schedulable = "false"

	_, err := v.ValidateUpdate(admissionCtxAs(bgUnitPlain), oldC, newC)
	if err == nil || !strings.Contains(err.Error(), "break-glass") {
		t.Fatalf("a plain user should be refused naming break-glass, got %v", err)
	}

	if _, err := v.ValidateUpdate(admissionCtxAs(bgUnitHolder), oldC, newC); err != nil {
		t.Fatalf("a break-glass holder should be allowed, got %v", err)
	}
}
