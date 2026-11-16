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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("a paused TalosOperation and the cluster lock", func() {
	It("releases the lock when it pauses with NoExecutor, so a second operation can proceed", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "nx-cp1")
		first := newOpTestOperation(cluster.Name)

		mustReconcileOperation(&TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient}, first)
		expectPausedBecause(fetchOperation(first), reasonOperationNoExecutor)

		_, held, err := opTestLock(cluster).Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "a pause that waits for a person holds no lock")

		second := newOpTestOperation(cluster.Name)
		r, fake := newOpTestReconciler(nil)
		mustReconcileOperation(r, second)
		Expect(fake.calls).NotTo(BeEmpty(), "the second operation must run, not wait on the first")
	})

	It("gives the cluster lock back when a shutdown has no Talos client, and says why", func() {
		f := newShutdownFixture()
		f.r.TalosClient = nil

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		_, err := reconcileOperation(f.r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

		expectLeaseFree(opTestLock(f.cluster))
		expectPausedBecause(fetchOperation(op), reasonOperationNoExecutor)
	})

	It("gives the cluster lock back when a shutdown cannot load the secrets bundle", func() {
		f := newShutdownFixture()
		f.r.ControllerNamespace = ""

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		_, err := reconcileOperation(f.r, op)

		expectLeaseFree(opTestLock(f.cluster))
		Expect(err).To(HaveOccurred())
	})

	It("gives the cluster lock back when a shutdown's stop request fails, so the retry takes it again", func() {
		f := newShutdownFixture()
		f.fakes[f.workers[0].Name].shutdownErr = errors.New("rpc failed")

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		_, err := reconcileOperation(f.r, op)
		Expect(err).To(HaveOccurred())

		expectLeaseFree(opTestLock(f.cluster))
	})
})
