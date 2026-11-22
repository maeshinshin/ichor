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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("automatic retry of a failed step (ADR 0174)", func() {
	var (
		cp1, cp2, cp3 *ichorv1alpha1.TalosMachine
		op            *ichorv1alpha1.TalosOperation
	)

	BeforeEach(func() {
		saved := operationRetryBase
		operationRetryBase = time.Millisecond

		DeferCleanup(func() { operationRetryBase = saved })

		cluster := newOpTestCluster()
		cp1 = newOpTestMachine(cluster.Name, "controlplane", "auto-cp1")
		cp2 = newOpTestMachine(cluster.Name, "controlplane", "auto-cp2")
		cp3 = newOpTestMachine(cluster.Name, "controlplane", "auto-cp3")
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")

		op = newOpTestOperation(cluster.Name)
	})

	// retryWhenDue waits out the scheduled retry, then reconciles once.
	retryWhenDue := func(r *TalosOperationReconciler) *ichorv1alpha1.TalosOperation {
		GinkgoHelper()

		got := fetchOperation(op)
		Expect(got.Status.AutoRetry).NotTo(BeNil())
		Expect(got.Status.AutoRetry.NextAt).NotTo(BeNil(), "a retry must be scheduled")
		time.Sleep(time.Until(got.Status.AutoRetry.NextAt.Time) + 20*time.Millisecond)

		return reconcileFetch(r, op)
	}

	It("retries a transient failure with backoff up to the limit, then pauses for a person", func() {
		failure := errors.New("etcdserver: request timed out")
		r, fake := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: failure}})

		got := reconcileFetch(r, op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.AutoRetry.Step).To(Equal(cp1.Name))
		Expect(fake.calls).To(HaveLen(1))

		for attempt := 1; attempt <= operationAutoRetryLimit; attempt++ {
			got = retryWhenDue(r)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused), "the retry failed again")
			Expect(got.Status.AutoRetry.Attempts).To(BeEquivalentTo(attempt))
			Expect(fake.calls).To(HaveLen(1 + attempt))
		}

		Expect(got.Status.AutoRetry.NextAt).To(BeNil(), "past the limit nothing is scheduled")
		Expect(got.Status.FailureReason).To(ContainSubstring("gave up"))

		time.Sleep(50 * time.Millisecond)
		mustReconcileOperation(r, op)
		Expect(fake.calls).To(HaveLen(1+operationAutoRetryLimit), "a step past the limit waits for a person")
	})

	It("retries a step that fails once and then succeeds,", func() {
		r, fake := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: errors.New("etcdserver: request timed out")}})

		reconcileFetch(r, op)

		fake.outcomes[cp1.Name] = advanceOutcome{done: true}
		got := retryWhenDue(r)
		Expect(got.Status.FailureReason).To(BeEmpty())
		Expect(got.Status.AutoRetry.Attempts).To(BeEquivalentTo(1))
		Expect(got.Status.Machines).NotTo(BeEmpty())
	})

	DescribeTable("pauses at once, and never retries, a failure no retry can clear",
		func(failure error) {
			r, fake := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: failure}})

			got := reconcileFetch(r, op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring(failure.Error()))
			Expect(got.Status.AutoRetry).To(BeNil(), "nothing is scheduled")

			time.Sleep(50 * time.Millisecond)
			mustReconcileOperation(r, op)
			Expect(fake.calls).To(HaveLen(1), "the step ran once and was not run again")
		},
		Entry("refused credentials", status.Error(codes.PermissionDenied, "node refused the credentials")),
		Entry("an answer that is no network failure", errors.New("node is not Ready; refusing to start an upgrade")),
		Entry("an Upgrade call that may have reached the node",
			&sendUnknownError{errors.New("etcdserver: request timed out")}),
	)

	It("never lifts a pause a person set, whatever retry is scheduled", func() {
		r, fake := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: errors.New("etcdserver: request timed out")}})

		reconcileFetch(r, op)

		got := fetchOperation(op)
		got.Spec.Paused = true
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		time.Sleep(50 * time.Millisecond)

		got = reconcileFetch(r, op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(fake.calls).To(HaveLen(1), "a person's pause stops the retry")
		Expect(pausedMessage(got)).To(Equal("spec.paused is true"))
	})

	It("shows the attempt count and the time of the next retry", func() {
		r, _ := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: errors.New("etcdserver: request timed out")}})

		got := reconcileFetch(r, op)
		Expect(got.Status.AutoRetry.NextAt).NotTo(BeNil())
		Expect(pausedMessage(got)).To(ContainSubstring("Retrying automatically at " + got.Status.AutoRetry.NextAt.UTC().Format(time.RFC3339)))
		Expect(pausedMessage(got)).To(ContainSubstring("(attempt 1 of 5)"))

		got = retryWhenDue(r)
		Expect(pausedMessage(got)).To(ContainSubstring("(attempt 2 of 5)"))
		Expect(got.Status.AutoRetry.Attempts).To(BeEquivalentTo(1))
	})

	It("starts the count again for another machine's step", func() {
		r, fake := newOpTestReconciler(map[string]advanceOutcome{cp1.Name: {err: errors.New("etcdserver: request timed out")}})

		reconcileFetch(r, op)
		retryWhenDue(r)

		fake.outcomes[cp1.Name] = advanceOutcome{done: true}
		fake.outcomes[cp2.Name] = advanceOutcome{err: errors.New("etcdserver: request timed out")}

		retryWhenDue(r) // cp1 is done
		got := reconcileFetch(r, op)
		Expect(got.Status.AutoRetry.Step).To(Equal(cp2.Name))
		Expect(got.Status.AutoRetry.Attempts).To(BeZero())
	})

	It("does not retry a node reset, or a node action already sent", func() {
		reset := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{
			Node: &ichorv1alpha1.TalosOperationNodeSpec{Action: ichorv1alpha1.NodeActionReset}}}
		reboot := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{
			Node: &ichorv1alpha1.TalosOperationNodeSpec{Action: ichorv1alpha1.NodeActionReboot}}}
		sent := reboot.DeepCopy()
		sent.Status.Node = &ichorv1alpha1.TalosOperationNodeStatus{Step: ichorv1alpha1.NodeStepRequested}

		Expect(nodeStepRetryable(reboot)).To(BeTrue())
		Expect(nodeStepRetryable(reset)).To(BeFalse())
		Expect(nodeStepRetryable(sent)).To(BeFalse())
	})
})
