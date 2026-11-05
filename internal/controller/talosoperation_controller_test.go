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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	return newOpTestOperationOfKind(clusterName, ichorv1alpha1.TalosOperationKindTalos)
}

func newOpTestOperationOfKind(clusterName, kind string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: "op-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Kind:       kind,
		},
	}
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func reconcileOperation(r *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: op.Namespace, Name: op.Name})
}

// mustReconcileOperation reconciles op once and expects no error.
func mustReconcileOperation(r *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation) {
	GinkgoHelper()

	_, err := reconcileOperation(r, op)
	Expect(err).NotTo(HaveOccurred())
}

// repeatStep runs step on op n times, each on the operation the last returned.
func repeatStep(
	step func(*ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation, op *ichorv1alpha1.TalosOperation, n int,
) *ichorv1alpha1.TalosOperation {
	for range n {
		op = step(op)
	}

	return op
}

// newOpTestReconciler wires a fakeAdvancer scripted with outcomes (nil: every machine is done at once).
func newOpTestReconciler(outcomes map[string]advanceOutcome) (*TalosOperationReconciler, *fakeAdvancer) {
	if outcomes == nil {
		outcomes = map[string]advanceOutcome{}
	}

	fake := &fakeAdvancer{outcomes: outcomes}

	return &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}, fake
}

// opTestLock is the cluster lock of cluster, for a test to take or inspect.
func opTestLock(cluster *ichorv1alpha1.TalosCluster) *clusterlock.Lock {
	return clusterlock.New(k8sClient, client.ObjectKeyFromObject(cluster), 0)
}

// pausedMessage is the message of op's Paused condition.
func pausedMessage(op *ichorv1alpha1.TalosOperation) string {
	return apimeta.FindStatusCondition(op.Status.Conditions, conditionOperationPaused).Message
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

// expectPausedBecause asserts a paused operation says why: a Paused condition carrying the reason
// and a message. failureReason stays empty for these, because it also means "a step failed, hold"
// to the controller, the CLI and the UI.
func expectPausedBecause(op *ichorv1alpha1.TalosOperation, reason string) {
	GinkgoHelper()

	cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionOperationPaused)
	Expect(cond).NotTo(BeNil(), "a paused operation must carry a reason")
	Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	Expect(cond.Reason).To(Equal(reason))
	Expect(cond.Message).NotTo(BeEmpty())
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
		mustReconcileOperation(r, op)

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationNoExecutor)
	})

	It("pauses with ClusterTransitioning and never calls Advance while the live control-plane count is even", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "tr-cp1")
		newOpTestMachine(cluster.Name, "controlplane", "tr-cp2")
		op := newOpTestOperation(cluster.Name)

		r, fake := newOpTestReconciler(nil)

		mustReconcileOperation(r, op)
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationTransitioning)
	})

	It("pauses with ClusterLockHeld and never calls Advance when another identity holds the cluster lock", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "lh-cp1")
		op := newOpTestOperation(cluster.Name)

		lock := opTestLock(cluster)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		mustReconcileOperation(r, op)
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationLockHeld)
		Expect(pausedMessage(got)).To(ContainSubstring(`"someone-else"`))
	})

	It("words a lock held by an unseen holder without an empty quoted name", func() {
		msg := (&clusterlock.HeldError{}).Error()
		Expect(msg).To(ContainSubstring("holder not visible yet"))
		Expect(msg).NotTo(ContainSubstring(`""`))
	})

	It("pauses with OperatorPaused and never calls Advance when spec.paused is true, but keeps the lock", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "pz-cp1")
		op := newOpTestOperation(cluster.Name)
		op.Spec.Paused = true
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		mustReconcileOperation(r, op)
		Expect(fake.calls).To(BeEmpty())

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		expectPausedBecause(got, reasonOperationPaused)

		lock := opTestLock(cluster)
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

		w3.Annotations = map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, w3)).To(Succeed())

		op := newOpTestOperation(cluster.Name)
		op.Spec.MaxUnavailable = 2
		Expect(k8sClient.Update(ctx, op)).To(Succeed())

		r, fake := newOpTestReconciler(nil)

		allCPs := []string{cp1.Name, cp2.Name, cp3.Name}

		// Reconcile 1, 2, 3: exactly one control plane per call, never a worker.
		for i := range allCPs {
			mustReconcileOperation(r, op)
			Expect(fake.calls).To(HaveLen(i + 1))
		}
		Expect(fake.calls).To(ContainElements(cp1.Name, cp2.Name, cp3.Name))

		// Reconcile 4: every control plane is Completed now, so workers
		// start -- up to maxUnavailable (2) of the 2 non-held workers
		// (w3 is held and must never appear).
		mustReconcileOperation(r, op)
		Expect(fake.calls).To(HaveLen(5))
		Expect(fake.calls[3:]).To(ContainElements(w1.Name, w2.Name))
		Expect(fake.calls).NotTo(ContainElement(w3.Name))

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		// Reconcile 5: both workers already Completed in status, nothing
		// left pending (w3 stays excluded, not pending), so this reconcile
		// calls Advance on nobody and reaches Completed.
		mustReconcileOperation(r, op)
		Expect(fake.calls).To(HaveLen(5), "no further Advance call once every eligible machine is already Completed")

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		lock := opTestLock(cluster)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "the lock must be released once every eligible machine is done")
	})

	It("records an Event when a machine starts and finishes, when it pauses and resumes, and on completion", func() {
		cluster := newOpTestCluster()
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "ev-cp1")
		op := newOpTestOperation(cluster.Name)

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp1.Name: {done: false}}}
		recorder := record.NewFakeRecorder(20)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance, Recorder: recorder}

		mustReconcileOperation(r, op)

		got := fetchOperation(op)
		Expect(got.Status.Machines).To(HaveLen(1))
		Expect(got.Status.Machines[0].StartedAt).NotTo(BeNil())
		Expect(got.Status.Machines[0].FinishedAt).To(BeNil())

		fake.outcomes[cp1.Name] = advanceOutcome{done: true}
		mustReconcileOperation(r, op)

		got = fetchOperation(op)
		Expect(got.Status.Machines[0].FinishedAt).NotTo(BeNil())

		mustReconcileOperation(r, op)

		got = fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(got.Status.FinishedAt).NotTo(BeNil())

		Expect(drainEvents(recorder)).To(Equal([]string{
			"Normal " + reasonMachineStarted + " machine " + cp1.Name + " started",
			"Normal " + reasonMachineFinished + " machine " + cp1.Name + " finished",
			"Normal " + reasonOperationComplete + " every eligible machine finished",
		}))
	})

	It("records a Warning when a step fails and an Event when it resumes", func() {
		cluster := newOpTestCluster()
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "evf-cp1")
		op := newOpTestOperation(cluster.Name)

		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp1.Name: {err: errors.New("boom")}}}
		recorder := record.NewFakeRecorder(20)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance, Recorder: recorder}

		mustReconcileOperation(r, op)
		Expect(drainEvents(recorder)).To(ContainElements(
			"Normal "+reasonMachineStarted+" machine "+cp1.Name+" started",
			"Warning "+reasonOperationStepFailed+" paused: machine \""+cp1.Name+"\": boom",
		))

		// A second reconcile of the same failure raises nothing new.
		mustReconcileOperation(r, op)
		Expect(drainEvents(recorder)).To(BeEmpty())
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

		r, fake := newOpTestReconciler(nil)

		mustReconcileOperation(r, op) // the control plane

		mustReconcileOperation(r, op) // exactly one worker, by label order
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
		r, fake := newOpTestReconciler(map[string]advanceOutcome{cp2.Name: {done: false, err: failure}})

		// cp1 succeeds (default outcome: done=true, no error).
		mustReconcileOperation(r, op)

		// The other control plane fails.
		mustReconcileOperation(r, op)

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
		mustReconcileOperation(r, op)
		Expect(fake.calls).To(HaveLen(callsBefore), "a paused, failed operation must not retry the failed step automatically")
	})

	Context("an explicit retry of a failed step", func() {
		// failedOperation leaves an operation Paused with a failureReason: the second of three
		// control planes failed once, the first is Completed.
		failedOperation := func(prefix string) (*ichorv1alpha1.TalosOperation, *fakeAdvancer, *TalosOperationReconciler, *ichorv1alpha1.TalosMachine) {
			cluster := newOpTestCluster()
			cp1 := newOpTestMachine(cluster.Name, "controlplane", prefix+"-cp1")
			cp2 := newOpTestMachine(cluster.Name, "controlplane", prefix+"-cp2")
			cp3 := newOpTestMachine(cluster.Name, "controlplane", prefix+"-cp3")
			setUpgradeOrder(cp1, "1")
			setUpgradeOrder(cp2, "2")
			setUpgradeOrder(cp3, "3")

			op := newOpTestOperation(cluster.Name)
			r, fake := newOpTestReconciler(map[string]advanceOutcome{cp2.Name: {done: false, err: errors.New("drain blocked")}})

			for range 2 {
				mustReconcileOperation(r, op)
			}

			Expect(fetchOperation(op).Status.FailureReason).To(ContainSubstring("drain blocked"))

			return op, fake, r, cp2
		}

		requestRetry := func(op *ichorv1alpha1.TalosOperation, at time.Time) {
			got := fetchOperation(op)
			got.Annotations = map[string]string{ichorv1alpha1.OperationRetryAnnotation: at.UTC().Format(time.RFC3339)}
			Expect(k8sClient.Update(ctx, got)).To(Succeed())
		}

		It("clears the failure, records lastRetry and runs the failed machine's step again", func() {
			op, fake, r, cp2 := failedOperation("retry")
			at := time.Now().Truncate(time.Second)

			fake.outcomes[cp2.Name] = advanceOutcome{done: true}
			callsBefore := len(fake.calls)

			requestRetry(op, at)

			mustReconcileOperation(r, op)

			got := fetchOperation(op)
			Expect(got.Status.FailureReason).To(BeEmpty())
			Expect(got.Status.LastRetry).NotTo(BeNil())
			Expect(got.Status.LastRetry.Unix()).To(Equal(at.Unix()))
			Expect(fake.calls[callsBefore:]).To(ContainElement(cp2.Name), "the failed machine's step must run again")
		})

		It("ignores a request that is not newer than lastRetry", func() {
			op, fake, r, _ := failedOperation("stale")
			at := time.Now().Truncate(time.Second)

			requestRetry(op, at)

			mustReconcileOperation(r, op) // the retry fails again, same outcome
			Expect(fetchOperation(op).Status.FailureReason).To(ContainSubstring("drain blocked"))
			Expect(fetchOperation(op).Status.LastRetry).NotTo(BeNil())

			callsBefore := len(fake.calls)

			requestRetry(op, at.Add(-time.Minute)) // older than lastRetry
			mustReconcileOperation(r, op)
			Expect(fake.calls).To(HaveLen(callsBefore), "a stale request must not run the step again")
			Expect(fetchOperation(op).Status.FailureReason).NotTo(BeEmpty())

			requestRetry(op, at) // the same one again
			mustReconcileOperation(r, op)
			Expect(fake.calls).To(HaveLen(callsBefore))
		})

		It("does nothing for a request on an operation that is not paused after a failure", func() {
			cluster := newOpTestCluster()
			newOpTestMachine(cluster.Name, "controlplane", "norequest-cp1")
			op := newOpTestOperation(cluster.Name)
			r, _ := newOpTestReconciler(nil)

			requestRetry(op, time.Now())

			mustReconcileOperation(r, op)
			Expect(fetchOperation(op).Status.LastRetry).To(BeNil(), "no failure, so the request is not acted on")
		})
	})

	It("resumes from status after a simulated controller restart, without re-advancing a completed machine", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "resume-cp1")
		w := newOpTestMachine(cluster.Name, "worker", "resume-w1")
		op := newOpTestOperation(cluster.Name)

		r1, fake1 := newOpTestReconciler(nil)

		mustReconcileOperation(r1, op) // advances cp
		Expect(fake1.calls).To(Equal([]string{cp.Name}))

		// A brand new reconciler, with its own fake and no memory of r1 at
		// all, simulating a restarted process picking the same
		// TalosOperation back up purely from its persisted status.
		r2, fake2 := newOpTestReconciler(nil)

		mustReconcileOperation(r2, op)
		Expect(fake2.calls).To(Equal([]string{w.Name}), "cp was already Completed in status; a resumed reconciler must not re-advance it")
	})
})

// drainEvents returns what a FakeRecorder holds right now, in order, as "Type Reason message".
func drainEvents(r *record.FakeRecorder) []string {
	var out []string

	for {
		select {
		case e := <-r.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}
