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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// testLeaseDuration is the lease these specs run under. A Lease records whole seconds, so one second is the
// shortest lease there is; testRenewEvery renews well inside it.
const (
	testLeaseDuration = time.Second
	testRenewEvery    = 100 * time.Millisecond
	stealWindow       = 2500 * time.Millisecond
	// someoneElse is a party other than the one under test: a lock holder, an operation named in a marker.
	someoneElse = "someone-else"
)

// tryStealLock tries to take the cluster lock as someone else every 50ms for window, and reports whether it got it.
func tryStealLock(cluster *ichorv1alpha1.TalosCluster, window time.Duration) bool {
	lock := clusterlock.New(k8sClient, client.ObjectKeyFromObject(cluster), testLeaseDuration)

	for deadline := time.Now().Add(window); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if lock.Acquire(ctx, someoneElse) == nil {
			return true
		}
	}

	return false
}

// setAckedClock sets clock condition on m to True, its transition time at since, the way a step's clock starts.
func setAckedClock(m *ichorv1alpha1.TalosMachine, clock string, since time.Time) {
	GinkgoHelper()

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: clock, Status: metav1.ConditionTrue, Reason: reasonUpgradeRequested, Message: "test",
	})
	apimeta.FindStatusCondition(m.Status.Conditions, clock).LastTransitionTime = metav1.NewTime(since)
	Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
}

// newRenewingReconciler wires an operation reconciler with the short lease and renewal cadence these specs need.
func newRenewingReconciler(advance AdvanceMachineFunc) *TalosOperationReconciler {
	return &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, Advance: advance,
		lockDuration: testLeaseDuration, renewInterval: testRenewEvery,
	}
}

// lockWaitBound bounds every wait on the Lease in these specs. It only fails a spec whose renewal never arrives;
// a loaded machine takes longer to get there but still passes.
const lockWaitBound = 30 * time.Second

// expectRenewedPastLease waits until the lock's renewTime is later than the renewTime it had on entry plus the
// lease it records. Only renewals made during the wait can move it that far, so this shows the lock outlived its
// lease without assuming how fast renewals arrive under load.
func expectRenewedPastLease(key client.ObjectKey, msg string) {
	GinkgoHelper()

	var first coordinationv1.Lease
	Expect(k8sClient.Get(ctx, key, &first)).To(Succeed())
	Expect(first.Spec.RenewTime).NotTo(BeNil())
	from := first.Spec.RenewTime.Time
	lease := time.Duration(ptr.Deref(first.Spec.LeaseDurationSeconds, 0)) * time.Second

	Eventually(func(g Gomega) {
		var cur coordinationv1.Lease
		g.Expect(k8sClient.Get(ctx, key, &cur)).To(Succeed())
		g.Expect(cur.Spec.RenewTime).NotTo(BeNil())
		g.Expect(cur.Spec.RenewTime.Time).To(BeTemporally(">", from.Add(lease)))
	}, lockWaitBound, testRenewEvery).Should(Succeed(), msg)
}

var _ = Describe("the cluster lock across the Advance loop", func() {
	It("holds the lock through an Advance call that outlasts the lease", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "renew-cp1")
		op := newOpTestOperation(cluster.Name)

		r := newRenewingReconciler(func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine,
			*ichorv1alpha1.TalosOperation,
		) (bool, error) {
			// Returns only once the lease would have lapsed without renewal, so the lock is held because renewal ran.
			expectRenewedPastLease(client.ObjectKeyFromObject(cluster),
				"the lease must be renewed while the Advance call runs, not only at the start of the loop")

			return true, nil
		})

		mustReconcileOperation(r, op)
	})

	It("stops renewing a step past its verify clock, so a stuck step lets the lease lapse", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "bound-cp1")
		op := newOpTestOperation(cluster.Name)

		// The node acknowledged its Upgrade call long ago and has not come back: its clock is past the verify timeout.
		setAckedClock(cp, conditionTalosUpgrade, time.Now().Add(-talosUpgradeVerifyTimeout-time.Minute))

		stolen := false
		r := newRenewingReconciler(func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine,
			*ichorv1alpha1.TalosOperation,
		) (bool, error) {
			stolen = tryStealLock(cluster, stealWindow)

			return false, nil
		})

		mustReconcileOperation(r, op)
		Expect(stolen).To(BeTrue(), "past the bound the renewal stops, so the lease lapses and another operation can take the lock")
	})

	It("stops the Advance loop and requeues, without pausing, once another holder takes the lock", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "lost-cp1")
		op := newOpTestOperation(cluster.Name)

		lockKey := client.ObjectKeyFromObject(cluster)
		sawCancel := false
		r := newRenewingReconciler(func(ctx context.Context, _ *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine,
			_ *ichorv1alpha1.TalosOperation,
		) (bool, error) {
			// Another holder takes the lease under the operation, so the next renewal is refused.
			var lease coordinationv1.Lease
			Expect(k8sClient.Get(ctx, lockKey, &lease)).To(Succeed())
			lease.Spec.HolderIdentity = ptr.To(someoneElse)
			Expect(k8sClient.Update(ctx, &lease)).To(Succeed())

			select {
			case <-ctx.Done():
				sawCancel = true

				return false, ctx.Err()
			case <-time.After(lockWaitBound):
				return true, nil
			}
		})

		result, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(sawCancel).To(BeTrue(), "a refused renewal must cancel the Advance loop's context")
		Expect(result.RequeueAfter).To(Equal(operationRequeueInterval))
		Expect(fetchOperation(op).Status.Phase).NotTo(Equal(ichorv1alpha1.TalosOperationPhasePaused),
			"losing the lock to another holder is contention, not a failed step")
	})
})

var _ = Describe("the cluster lock across a storage evacuation longer than one pass", func() {
	// evacuatingOperation is a Talos upgrade of one node whose storage evacuation (timeout, replicas still
	// remaining) has begun, reconciled by an operation reconciler that runs the real upgrade step.
	evacuatingOperation := func(suffix string, timeout time.Duration) (
		*TalosOperationReconciler, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation,
	) {
		cluster, cp := newUpgradeNode(suffix, true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target:    &evacuationTarget{hook: hook, timeout: timeout, namespace: defaultLonghornNamespace},
			remaining: 3,
		}
		adv := newUpgradeAdvancer(&fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}, evacuator)

		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}
		op := newOpTestOperation(cluster.Name)
		mustReconcileOperation(r, op) // begins the evacuation

		return r, cluster, cp, op
	}

	// evacuatingFor moves the evacuation's clock back, as if it had begun that long ago.
	evacuatingFor := func(cp *ichorv1alpha1.TalosMachine, ago time.Duration) {
		got := fetchMachine(cp)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionStorageEvacuation)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-ago))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
	}

	It("holds the lock and does not pause past the renewal ceiling while evacuationTimeout is still running", func() {
		r, cluster, cp, op := evacuatingOperation("renew-evac-held", 2*time.Hour)

		evacuatingFor(cp, operationRenewCeiling+15*time.Minute)

		result, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(operationRequeueInterval))
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		var lease coordinationv1.Lease
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &lease)).To(Succeed())
		Expect(ptr.Deref(lease.Spec.HolderIdentity, "")).To(Equal(operationLockIdentity(op)))
	})

	It("pauses once the evacuation passes its own evacuationTimeout, and the pause releases the lock", func() {
		r, cluster, cp, op := evacuatingOperation("renew-evac-paused", 2*time.Hour)

		evacuatingFor(cp, 2*time.Hour+time.Minute)

		got := reconcileFetch(r, op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("timed out"))

		var lease coordinationv1.Lease
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &lease)
		Expect(err).To(MatchError(apierrors.IsNotFound, "a paused operation releases the cluster lock (ADR 0162)"))
	})
})
