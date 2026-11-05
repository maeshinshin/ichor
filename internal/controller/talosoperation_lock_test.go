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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Two TalosOperations created outside the UI/API service (kubectl, GitOps) must not run their
// disruptive steps at the same time: each takes the cluster lock under its own identity
// (ADR 0068 amendment 2026-11-04).
var _ = Describe("two TalosOperations on one cluster", func() {
	It("makes the second wait, naming the first, until the first has released the cluster lock", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "two-cp1")
		first := newOpTestOperation(cluster.Name)
		second := newOpTestOperation(cluster.Name)

		// The first operation's machine is still rebooting, so it keeps the lock between reconciles.
		firstReconciler, firstFake := newOpTestReconciler(map[string]advanceOutcome{cp.Name: {done: false}})

		mustReconcileOperation(firstReconciler, first)
		Expect(firstFake.calls).To(HaveLen(1))

		secondReconciler, secondFake := newOpTestReconciler(nil)

		result, err := reconcileOperation(secondReconciler, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondFake.calls).To(BeEmpty(), "a second operation must not run a disruptive step while the first holds the lock")
		Expect(result.RequeueAfter).To(Equal(operationRequeueInterval))

		got := fetchOperation(second)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationLockHeld)
		Expect(pausedMessage(got)).
			To(ContainSubstring(first.Name), "the message names the operation that holds the lock")

		// The first finishes: the lock is freed and the second proceeds.
		firstFake.outcomes = map[string]advanceOutcome{}
		mustReconcileOperation(firstReconciler, first)
		mustReconcileOperation(firstReconciler, first)
		Expect(fetchOperation(first).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		mustReconcileOperation(secondReconciler, second)
		Expect(secondFake.calls).To(HaveLen(1))
	})

	It("lets the same operation back in across reconciles and a controller restart", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "re-cp1")
		op := newOpTestOperation(cluster.Name)

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp.Name: {done: false}}}
		for range 3 { // a fresh reconciler each time is a restarted controller
			r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}
			mustReconcileOperation(r, op)
		}
		Expect(fake.calls).To(HaveLen(3))
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
	})

	// A lease whose holder is gone or has stopped for good does not make anyone wait out its five minutes.
	It("takes over a lease held by a deleted operation, and by one paused on a failed step", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "gone-cp1")
		lock := opTestLock(cluster)

		deleted := newOpTestOperation(cluster.Name)
		Expect(lock.Acquire(ctx, operationLockIdentity(deleted))).To(Succeed())
		Expect(k8sClient.Delete(ctx, deleted)).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		next := newOpTestOperation(cluster.Name)
		mustReconcileOperation(r, next)
		Expect(fake.calls).To(HaveLen(1), "the lease of a deleted operation is not waited for")

		failed := newOpTestOperation(cluster.Name)
		failed.Status.Phase = ichorv1alpha1.TalosOperationPhasePaused
		failed.Status.FailureReason = "boom"
		Expect(k8sClient.Status().Update(ctx, failed)).To(Succeed())
		Expect(lock.Release(ctx, operationLockIdentity(next))).To(Succeed())
		Expect(lock.Acquire(ctx, operationLockIdentity(failed))).To(Succeed())

		another := newOpTestOperation(cluster.Name)
		mustReconcileOperation(r, another)
		Expect(fake.calls).To(HaveLen(2), "an operation paused on a failed step does not hold the cluster")
	})

	// A lease the old build left under the shared "ichor-controller" identity belongs to nobody this build
	// can name (machine removals and joins used it before they got their own identity, ADR 0026
	// follow-up): the operation waits, and the lease ages out within clusterlock.DefaultDuration because
	// nothing renews it any more.
	It("waits for a lease left under the old shared identity", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "old-cp1")
		op := newOpTestOperation(cluster.Name)

		lock := opTestLock(cluster)
		Expect(lock.Acquire(ctx, controllerIdentity)).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		// This operation has started nothing yet, so the old lease is not its own: it waits.
		mustReconcileOperation(r, op)
		Expect(fake.calls).To(BeEmpty())
		expectPausedBecause(fetchOperation(op), reasonOperationLockHeld)
	})

	// A machine join or removal now takes the lock under the machine's own identity (ADR 0026 follow-up).
	It("waits for a machine that holds the lock, naming it, and takes over the lease of one that is gone", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "mh-cp1")
		op := newOpTestOperation(cluster.Name)

		holder := newOpTestMachine(cluster.Name, "worker", "mh-w1")
		lock := opTestLock(cluster)
		Expect(lock.Acquire(ctx, machineLockIdentity(holder))).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		mustReconcileOperation(r, op)
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		expectPausedBecause(got, reasonOperationLockHeld)
		Expect(pausedMessage(got)).
			To(And(ContainSubstring("TalosMachine"), ContainSubstring(holder.Name)))

		Expect(k8sClient.Delete(ctx, holder)).To(Succeed())

		mustReconcileOperation(r, op)
		Expect(fake.calls).To(HaveLen(1), "the lease of a deleted machine is not waited for")
	})
})
