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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
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
		firstFake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp.Name: {done: false}}}
		firstReconciler := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: firstFake.Advance}

		_, err := reconcileOperation(firstReconciler, first)
		Expect(err).NotTo(HaveOccurred())
		Expect(firstFake.calls).To(HaveLen(1))

		secondFake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		secondReconciler := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: secondFake.Advance}

		result, err := reconcileOperation(secondReconciler, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondFake.calls).To(BeEmpty(), "a second operation must not run a disruptive step while the first holds the lock")
		Expect(result.RequeueAfter).To(Equal(operationRequeueInterval))

		got := fetchOperation(second)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationLockHeld)
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionOperationPaused).Message).
			To(ContainSubstring(first.Name), "the message names the operation that holds the lock")

		// The first finishes: the lock is freed and the second proceeds.
		firstFake.outcomes = map[string]advanceOutcome{}
		_, err = reconcileOperation(firstReconciler, first)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconcileOperation(firstReconciler, first)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchOperation(first).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		_, err = reconcileOperation(secondReconciler, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondFake.calls).To(HaveLen(1))
	})

	It("lets the same operation back in across reconciles and a controller restart", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "re-cp1")
		op := newOpTestOperation(cluster.Name)

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp.Name: {done: false}}}
		for range 3 { // a fresh reconciler each time is a restarted controller
			r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}
			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(fake.calls).To(HaveLen(3))
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
	})

	// A lease whose holder is gone or has stopped for good does not make anyone wait out its five minutes.
	It("takes over a lease held by a deleted operation, and by one paused on a failed step", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "gone-cp1")
		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)

		deleted := newOpTestOperation(cluster.Name)
		Expect(lock.Acquire(ctx, operationLockIdentity(deleted))).To(Succeed())
		Expect(k8sClient.Delete(ctx, deleted)).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		next := newOpTestOperation(cluster.Name)
		_, err := reconcileOperation(r, next)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(1), "the lease of a deleted operation is not waited for")

		failed := newOpTestOperation(cluster.Name)
		failed.Status.Phase = ichorv1alpha1.TalosOperationPhasePaused
		failed.Status.FailureReason = "boom"
		Expect(k8sClient.Status().Update(ctx, failed)).To(Succeed())
		Expect(lock.Release(ctx, operationLockIdentity(next))).To(Succeed())
		Expect(lock.Acquire(ctx, operationLockIdentity(failed))).To(Succeed())

		another := newOpTestOperation(cluster.Name)
		_, err = reconcileOperation(r, another)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(2), "an operation paused on a failed step does not hold the cluster")
	})

	// A lease the old build left under the shared "ichor-controller" identity is indistinguishable from
	// a machine removal or join, which still use it: the operation waits, and the lease ages out within
	// clusterlock.DefaultDuration because nothing renews it any more.
	It("waits for a lease held by the shared machine identity", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "old-cp1")
		op := newOpTestOperation(cluster.Name)

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(lock.Acquire(ctx, controllerIdentity)).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		// This operation has started nothing yet, so the old lease is not its own: it waits.
		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(BeEmpty())
		expectPausedBecause(fetchOperation(op), reasonOperationLockHeld)
	})
})
