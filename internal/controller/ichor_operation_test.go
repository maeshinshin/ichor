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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("TalosOperationReconciler, kind ichor", func() {
	newIchorOp := func(clusterName string) *ichorv1alpha1.TalosOperation {
		newOpTestMachine(clusterName, "controlplane", "ich-cp")
		newOpTestMachine(clusterName, "worker", "ich-w")

		return newOpTestOperationOfKind(clusterName, ichorv1alpha1.TalosOperationKindIchor)
	}

	It("runs the executor with no machine, records no machines, and pauses on its failure", func() {
		op := newIchorOp(newOpTestCluster().Name)

		var failure error

		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: testIchorNamespace, Advance: func(
			_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			Expect(m).To(BeNil())

			return false, failure
		}}

		res, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).NotTo(BeZero())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(got.Status.Machines).To(BeEmpty())

		failure = errors.New("job failed")
		mustReconcileOperation(r, op)

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("job failed"))
		Expect(got.Status.Machines).To(BeEmpty())
	})

	It("pauses with the installer image's reason and a failureReason, so it is not retried by itself", func() {
		op := newIchorOp(newOpTestCluster().Name)

		refusal := &installerImageError{reason: reasonInstallerImageUnverified, err: errors.New("has no cosign signature")}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: testIchorNamespace, Advance: func(
			context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			return false, refusal
		}}

		mustReconcileOperation(r, op)

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("has no cosign signature"))
		Expect(got.Status.Conditions).To(ContainElement(And(
			HaveField("Type", conditionOperationPaused), HaveField("Reason", reasonInstallerImageUnverified))))

		// Reconciled again without a retry request, it stays paused and runs nothing.
		refusal.err = errors.New("must not run")
		mustReconcileOperation(r, op)
		Expect(fetchOperation(op).Status.FailureReason).To(ContainSubstring("has no cosign signature"))
	})

	It("completes with no machines once the executor is done", func() {
		op := newIchorOp(newOpTestCluster().Name)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: func(
			context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			return true, nil
		}}

		mustReconcileOperation(r, op)

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(got.Status.Machines).To(BeEmpty())
	})

	It("shows what it waits for in status.message while InProgress, and clears it when done", func() {
		op := newIchorOp(newOpTestCluster().Name)

		labels := map[string]string{controlPlaneLabel: valueControllerMgr}
		d := &appsv1.Deployment{
			Name: "msg-controller", Namespace: testIchorNamespace, Labels: labels,
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"msg-test": "x"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"msg-test": "x"}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "registry.example/c:v1"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, d))).To(Succeed()) })
		setRollout(d, false)

		done := false
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: testIchorNamespace, Advance: func(
			context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			return done, nil
		}}

		mustReconcileOperation(r, op)

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(got.Status.Message).To(Equal("waiting for the controller Deployment to finish rolling out (0 of 1 updated replicas are available)"))

		// An unchanged message is not written again.
		version := got.ResourceVersion
		mustReconcileOperation(r, op)
		Expect(fetchOperation(op).ResourceVersion).To(Equal(version))

		done = true
		mustReconcileOperation(r, op)

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(got.Status.Message).To(BeEmpty())
	})
})
