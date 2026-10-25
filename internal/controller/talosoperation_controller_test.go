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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

// newOpTestCluster creates an approved-free TalosCluster for these tests;
// control planes and workers are added separately via newOpTestMachine so
// each test controls exactly how many of each exist.
func newOpTestCluster() *ichorv1alpha1.TalosCluster {
	return newTestCluster("does-not-exist", "v1.14")
}

// newOpTestMachine creates an approved TalosMachine of role for cluster,
// named distinctly (name feeds only the identity UUID -- GenerateName
// still assigns the real object name -- so it never collides with another
// fixture's UUID-shaped literal under TestFixtureUUIDsAreUnique, since
// this is not UUID-shaped to begin with).
func newOpTestMachine(clusterName, role, name string) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		GenerateName: "op-machine-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + name},
			Endpoint:      "10.0.0.30:50000",
			Role:          role,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

// setUpgradeOrder sets ichorv1alpha1.TalosMachineUpgradeOrderLabel on m to
// value and persists it, so a test can force a deterministic
// sortByUpgradeOrder result instead of depending on GenerateName's
// unpredictable, server-assigned object name as the tiebreak.
func setUpgradeOrder(m *ichorv1alpha1.TalosMachine, value string) {
	m.Labels = map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: value}
	Expect(k8sClient.Update(ctx, m)).To(Succeed())
}

func newOpTestOperation(clusterName string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: "op-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Kind:       ichorv1alpha1.TalosOperationKindTalos,
		},
	}
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func reconcileOperation(r *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: op.Namespace, Name: op.Name})
}

func fetchOperation(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	var got ichorv1alpha1.TalosOperation
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got)).To(Succeed())

	return &got
}

// advanceOutcome is one machine's scripted result for a fakeAdvancer.
type advanceOutcome struct {
	done bool
	err  error
}

// fakeAdvancer is an AdvanceMachineFunc test double: it records every
// machine name it was called for, in call order, and returns outcomes
// scripted by name -- defaulting to an immediate, successful done=true for
// any name not scripted.
type fakeAdvancer struct {
	calls    []string
	outcomes map[string]advanceOutcome
}

func (f *fakeAdvancer) Advance(
	_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation,
) (bool, error) {
	f.calls = append(f.calls, m.Name)

	if o, ok := f.outcomes[m.Name]; ok {
		return o.done, o.err
	}

	return true, nil
}

var _ = Describe("TalosOperationReconciler", func() {
	It("ignores a TalosOperation that no longer exists", func() {
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient}

		result, err := r.Reconcile(ctx, ctrl.Request{Namespace: tmNamespace, Name: "no-such-op"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
	})

	It("pauses with NoExecutor and never calls Advance when none is wired up", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "ne-cp1")
		op := newOpTestOperation(cluster.Name)

		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient}
		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.Conditions).To(BeEmpty()) // this phase sets no Conditions, only Phase/FailureReason
	})

	It("pauses with ClusterTransitioning and never calls Advance while the live control-plane count is even", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "tr-cp1")
		newOpTestMachine(cluster.Name, "controlplane", "tr-cp2")
		op := newOpTestOperation(cluster.Name)

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
	})

	It("pauses with ClusterLockHeld and never calls Advance when another identity holds the cluster lock", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "lh-cp1")
		op := newOpTestOperation(cluster.Name)

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
	})

	It("pauses with OperatorPaused and never calls Advance when spec.paused is true, but keeps the lock", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "pz-cp1")
		op := newOpTestOperation(cluster.Name)
		op.Spec.Paused = true
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue(), "spec.paused halts progress but must not release the cluster lock")
	})

	It("advances control planes one at a time before touching any worker, respects maxUnavailable, excludes a held machine, and completes", func() {
		cluster := newOpTestCluster()
		// Three, not two: a live, approved control-plane count of two is
		// itself "a replacement is in progress" (transitioningControlPlanes),
		// which acquireDisruptiveLock refuses -- unrelated to what this
		// test is about, so it stays at the declared-topology-valid odd
		// counts (1, 3, 5).
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "full-cp1")
		cp2 := newOpTestMachine(cluster.Name, "controlplane", "full-cp2")
		cp3 := newOpTestMachine(cluster.Name, "controlplane", "full-cp3")
		w1 := newOpTestMachine(cluster.Name, "worker", "full-w1")
		w2 := newOpTestMachine(cluster.Name, "worker", "full-w2")
		w3 := newOpTestMachine(cluster.Name, "worker", "full-w3")

		w3.Annotations = map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: "true"}
		Expect(k8sClient.Update(ctx, w3)).To(Succeed())

		op := newOpTestOperation(cluster.Name)
		op.Spec.MaxUnavailable = 2
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		allCPs := []string{cp1.Name, cp2.Name, cp3.Name}

		// Reconcile 1, 2, 3: exactly one control plane per call, never a worker.
		for i := range allCPs {
			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.calls).To(HaveLen(i + 1))
		}
		Expect(fake.calls).To(ContainElements(cp1.Name, cp2.Name, cp3.Name))

		// Reconcile 4: every control plane is Completed now, so workers
		// start -- up to maxUnavailable (2) of the 2 non-held workers
		// (w3 is held and must never appear).
		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(5))
		Expect(fake.calls[3:]).To(ContainElements(w1.Name, w2.Name))
		Expect(fake.calls).NotTo(ContainElement(w3.Name))

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		// Reconcile 5: both workers already Completed in status, nothing
		// left pending (w3 stays excluded, not pending), so this reconcile
		// calls Advance on nobody and reaches Completed.
		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(5), "no further Advance call once every eligible machine is already Completed")

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "the lock must be released once every eligible machine is done")
	})

	It("orders same-tier machines by the upgrade-order label, not creation order", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "lo-cp1")
		wLater := newOpTestMachine(cluster.Name, "worker", "lo-w-later")
		wFirst := newOpTestMachine(cluster.Name, "worker", "lo-w-first")

		wLater.Labels = map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: "2"}
		Expect(k8sClient.Update(ctx, wLater)).To(Succeed())
		wFirst.Labels = map[string]string{ichorv1alpha1.TalosMachineUpgradeOrderLabel: "1"}
		Expect(k8sClient.Update(ctx, wFirst)).To(Succeed())

		op := newOpTestOperation(cluster.Name)
		op.Spec.MaxUnavailable = 1
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		_, err := reconcileOperation(r, op) // the control plane
		Expect(err).NotTo(HaveOccurred())

		_, err = reconcileOperation(r, op) // exactly one worker, by label order
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(2))
		Expect(fake.calls[1]).To(Equal(wFirst.Name), "the worker labeled \"1\" must advance before the one labeled \"2\"")
	})

	It("pauses on a failed step without undoing an already-completed machine, and does not retry automatically", func() {
		cluster := newOpTestCluster()
		// Three, not two, for the same reason as the completion test above:
		// a live, approved control-plane count of two is itself
		// "a replacement is in progress" and gets refused before Advance is
		// ever called, which is not what this test is about.
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "fail-cp1")
		cp2 := newOpTestMachine(cluster.Name, "controlplane", "fail-cp2")
		cp3 := newOpTestMachine(cluster.Name, "controlplane", "fail-cp3")

		// GenerateName means the real object names are server-assigned and
		// not predictable, so force the order explicitly rather than
		// relying on the name tiebreak in sortByUpgradeOrder.
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")

		op := newOpTestOperation(cluster.Name)

		failure := errors.New("talos upgrade API: dial refused")
		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp2.Name: {done: false, err: failure}}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		// cp1 succeeds (default outcome: done=true, no error).
		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		// The other control plane fails.
		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring(failure.Error()))

		completed := 0
		for _, ms := range got.Status.Machines {
			if ms.Phase == ichorv1alpha1.TalosOperationMachinePhaseCompleted {
				completed++
			}
		}
		Expect(completed).To(Equal(1), "the machine that succeeded before the failure must stay Completed")

		callsBefore := len(fake.calls)
		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.calls).To(HaveLen(callsBefore), "a paused, failed operation must not retry the failed step automatically")
	})

	It("resumes from status after a simulated controller restart, without re-advancing a completed machine", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "resume-cp1")
		w := newOpTestMachine(cluster.Name, "worker", "resume-w1")
		op := newOpTestOperation(cluster.Name)

		fake1 := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r1 := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake1.Advance}

		_, err := reconcileOperation(r1, op) // advances cp
		Expect(err).NotTo(HaveOccurred())
		Expect(fake1.calls).To(Equal([]string{cp.Name}))

		// A brand new reconciler, with its own fake and no memory of r1 at
		// all, simulating a restarted process picking the same
		// TalosOperation back up purely from its persisted status.
		fake2 := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r2 := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake2.Advance}

		_, err = reconcileOperation(r2, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake2.calls).To(Equal([]string{w.Name}), "cp was already Completed in status; a resumed reconciler must not re-advance it")
	})
})
