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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("TalosOperationReconciler, kind ichor", func() {
	newIchorOp := func(clusterName string) *ichorv1alpha1.TalosOperation {
		newOpTestMachine(clusterName, "controlplane", "ich-cp")
		newOpTestMachine(clusterName, "worker", "ich-w")
		op := newOpTestOperationOfKind(clusterName, ichorv1alpha1.TalosOperationKindIchor)

		return op
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
		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("job failed"))
		Expect(got.Status.Machines).To(BeEmpty())
	})

	It("completes with no machines once the executor is done", func() {
		op := newIchorOp(newOpTestCluster().Name)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: func(
			context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			return true, nil
		}}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(got.Status.Machines).To(BeEmpty())
	})
})
