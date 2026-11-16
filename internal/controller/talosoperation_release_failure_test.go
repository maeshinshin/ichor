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

	coordinationv1 "k8s.io/api/coordination/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("setPhase when the cluster lock cannot be released", func() {
	It("records the pause when releasing the lock fails, returns that error, and leaves the cluster to the next operation", func() {
		cluster := newOpTestCluster()
		m := newOpTestMachine(cluster.Name, "controlplane", "rf-cp1")
		op := newOpTestOperation(cluster.Name)

		r, _ := newOpTestReconciler(map[string]advanceOutcome{m.Name: {err: errors.New("boom")}})
		r.Client = interceptor.NewClient(watchClient(), interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, ok := obj.(*coordinationv1.Lease); ok {
					return errors.New("lease delete refused")
				}

				return c.Delete(ctx, obj, opts...)
			},
		})

		_, err := reconcileOperation(r, op)
		Expect(err).To(MatchError(ContainSubstring("lease delete refused")))

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused), "the transition is recorded before the lock is released")
		expectPausedBecause(got, reasonOperationStepFailed)
		Expect(got.Status.FailureReason).To(ContainSubstring("boom"))

		holder, held, err := opTestLock(cluster).Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue())
		Expect(holder).To(Equal(operationLockIdentity(op)))

		// The paused operation does not retry the release (only an explicit retry moves it), so a lease left
		// behind is freed by whoever needs the cluster next: its holder is Paused on a failed step.
		next := newOpTestOperation(cluster.Name)
		other, fake := newOpTestReconciler(nil)
		mustReconcileOperation(other, next)
		Expect(fake.calls).NotTo(BeEmpty())
	})
})
