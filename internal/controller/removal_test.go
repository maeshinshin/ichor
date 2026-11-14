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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

// newTestControlPlane creates an approved control-plane TalosMachine in
// clusterName, distinct per suffix so several can coexist in one test and
// be counted correctly by countApprovedControlPlanes.
func newTestControlPlane(clusterName, suffix string) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		GenerateName: "cp-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-cp-" + clusterName + "-" + suffix},
			Endpoint:      "10.0.2." + suffix + ":50000",
			Role:          "controlplane",
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

// newTestClusterWithCount is newTestCluster with an explicit
// controlPlane.count, for the count-guard tests below. secretName is
// almost always "does-not-exist": every refusal case in this file is
// decided before removeEtcdMember ever loads a bundle, so only the tests
// that expect a removal to actually reach RemovalApproved pass a real one.
func newTestClusterWithCount(count int32, secretName string) *ichorv1alpha1.TalosCluster {
	spec := newClusterSpec(secretName, testK8sContract)
	spec.Endpoint.Address = "10.0.0.6"
	spec.KubernetesVersion = "v1.34.0"
	spec.ControlPlane.Count = count

	cluster := &ichorv1alpha1.TalosCluster{GenerateName: testClusterGenerate, Namespace: tmNamespace, Spec: spec}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	return cluster
}

// removalReadyReconciler returns a TalosMachineReconciler whose TalosClient
// always reports the departing machine already absent from etcd (the
// simplest fake that lets a removal that passes its quorum/count checks
// reach completeRemoval, for tests that are about those checks, not about
// removeEtcdMember's own behavior -- that gets its own tests below), and
// the real secrets bundle Secret's name its caller's TalosCluster needs to
// reference for loadBundle to succeed.
func removalReadyReconciler() (*TalosMachineReconciler, string) {
	// nodeName: machines here have no spec.hostname, and a control plane whose name cannot be
	// resolved is refused.
	fake := &fakeTalosClient{nodeName: "removal-ready-node"}
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)

	r := &TalosMachineReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: clientFactory(fake),
	}

	return r, secret.Name
}

var _ = Describe("TalosMachineReconciler removal", func() {
	It("gives an approved control plane the removal finalizer on its first reconcile, and nothing else", func() {
		cluster := newTestClusterWithCount(1, "does-not-exist")
		cp := newTestControlPlane(cluster.Name, "1")

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		reconcileOK(r, cp)

		got := fetchMachine(cp)
		Expect(controllerutil.ContainsFinalizer(got, machineRemovalFinalizer)).To(BeTrue())
		Expect(got.Status.Conditions).To(BeEmpty())
	})

	It("never gives a worker or an unapproved machine the removal finalizer", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		worker := newTestMachine(cluster.Name, true)
		pending := newTestControlPlane(cluster.Name, "2")
		pending.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending
		Expect(k8sClient.Update(ctx, pending)).To(Succeed())

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		reconcileOK(r, worker)
		reconcileOK(r, pending)

		Expect(controllerutil.ContainsFinalizer(fetchMachine(worker), machineRemovalFinalizer)).To(BeFalse())
		Expect(controllerutil.ContainsFinalizer(fetchMachine(pending), machineRemovalFinalizer)).To(BeFalse())

		// Both delete immediately: no finalizer ever intercepts them.
		Expect(k8sClient.Delete(ctx, worker)).To(Succeed())
		Expect(k8sClient.Delete(ctx, pending)).To(Succeed())
		reconcileOK(r, worker)
	})

	// The four failure modes docs/design/architecture.md's "Nodes" names for
	// the removal sequence, and how each is answered now that Reset has landed too: naming the partitions to wipe is
	// machineryTalosClient.Reset's own fixed SystemPartitionsToWipe list
	// (STATE, EPHEMERAL -- never empty, never the implicit wipe-everything);
	// the fire-and-forget double-reset is resetNode's own
	// already-requested check (reset_test.go); a refusal still never
	// reaches Talos at all (checked before drain, removeEtcdMember or
	// resetNode); and removeEtcdMember's own idempotency
	// (etcd_removal_test.go) is this phase's answer to "removing an
	// already-removed etcd member reports not-found, which counts as
	// success."
	Describe("the four failure modes architecture.md calls out", func() {
		It("refuses on its own refusal, never by asking Talos (there is no Talos call here at all)", func() {
			cluster := newTestClusterWithCount(1, "does-not-exist")
			cp := newTestControlPlane(cluster.Name, "3")

			r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
			reconcileOK(r, cp)

			Expect(k8sClient.Delete(ctx, cp)).To(Succeed())
			reconcileOK(r, cp)

			got := fetchMachine(cp)
			cond := expectRemoval(got, reasonRemovalRefused)
			Expect(cond.Message).To(ContainSubstring("last control plane"))

			// The finalizer is still there: refusing means staying, not disappearing.
			Expect(controllerutil.ContainsFinalizer(got, machineRemovalFinalizer)).To(BeTrue())
		})

		It("is resumable from status: a fresh reconciler picking up mid-operation does not fail or double-act", func() {
			cluster := newTestClusterWithCount(1, "does-not-exist")
			cp1 := newTestControlPlane(cluster.Name, "4")
			cp2 := newTestControlPlane(cluster.Name, "5")

			r1 := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
			reconcileOK(r1, cp1)
			reconcileOK(r1, cp2)

			Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())

			// Simulate the lock surviving a crash: acquire it as if r1's
			// first reconcile pass got as far as taking the lock, then
			// "restart" as a brand new reconciler instance (r2) with no
			// memory of r1 at all, same identity (clusterlock's own
			// contract: identity, not process, is what resumption keys on).
			lock := opTestLock(cluster)
			Expect(lock.Acquire(ctx, machineLockIdentity(cp1))).To(Succeed())

			r2 := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
			_, err := reconcileMachine(r2, cp1)
			Expect(err).NotTo(HaveOccurred(), "a fresh reconciler must resume its own identity's lock, not be told it's held")

			got := fetchMachine(cp1)
			cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
			Expect(cond).NotTo(BeNil())
			// 2 -> 1 is quorum of 2, refused -- cp1 and cp2 coexist, so this
			// resumed reconcile reaches a verdict rather than erroring out.
			Expect(cond.Reason).To(Equal(reasonRemovalRefused))

			Expect(lock.Release(ctx, machineLockIdentity(cp1))).To(Succeed())
		})
	})

	It("refuses a removal that would break quorum, and allows one that doesn't", func() {
		r, secretName := removalReadyReconciler()
		cluster := newTestClusterWithCount(1, secretName)
		cp1 := newTestControlPlane(cluster.Name, "6")
		cp2 := newTestControlPlane(cluster.Name, "7")
		cp3 := newTestControlPlane(cluster.Name, "8")

		reconcileOK(r, cp1, cp2, cp3)

		// 3 -> 2: quorum of 3 is 2, allowed (desiredCount is 1, so the count
		// guard doesn't refuse it either). cp1 has no Hostname set, so
		// removeEtcdMember's fake sees it as already absent from etcd
		// (etcdMemberID("") never matches) and the removal completes in
		// full: the object is actually gone, not left holding a
		// "RemovalApproved" condition the way an unimplemented etcd
		// removal used to leave it.
		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		reconcileOK(r, cp1)
		Expect(machineGone(cp1)).To(BeTrue(), "a removal that passes every check now completes for real")

		// A separate pair: cp1 is gone now, so this needs fresh machines
		// rather than reusing cp2/cp3 from the group cp1 came from.
		cluster2 := newTestClusterWithCount(1, "does-not-exist")
		cpA := newTestControlPlane(cluster2.Name, "6b")
		cpB := newTestControlPlane(cluster2.Name, "7b")
		reconcileOK(r, cpA, cpB)

		// 2 -> 1: quorum of 2 is 2, refused.
		Expect(k8sClient.Delete(ctx, cpA)).To(Succeed())
		reconcileOK(r, cpA)
		cond := expectRemoval(fetchMachine(cpA), reasonRemovalRefused)
		Expect(cond.Message).To(ContainSubstring("quorum"))
	})

	It("refuses dropping below spec.controlPlane.count, and allows the add-then-remove replacement", func() {
		r, secretName := removalReadyReconciler()
		cluster := newTestClusterWithCount(3, secretName)
		cp1 := newTestControlPlane(cluster.Name, "9")
		cp2 := newTestControlPlane(cluster.Name, "10")
		cp3 := newTestControlPlane(cluster.Name, "11")

		reconcileOK(r, cp1, cp2, cp3)

		// 3 -> 2, below the declared count of 3: refused even though quorum
		// of 3 (which is 2) would itself allow it.
		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		reconcileOK(r, cp1)
		cond := expectRemoval(fetchMachine(cp1), reasonRemovalRefused)
		Expect(cond.Message).To(ContainSubstring("declared count"))

		// Approve a 4th control plane first (3 -> 4), then removing one of
		// the original three (4 -> 3) meets the declared count.
		cp4 := newTestControlPlane(cluster.Name, "12")
		reconcileOK(r, cp4)

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		reconcileOK(r, cp1)
		// cp1 has no Hostname set, so removeEtcdMember's fake sees it as
		// already absent and the removal completes in full this time.
		Expect(machineGone(cp1)).To(BeTrue(), "4 -> 3 meets the declared count, so this removal completes for real")
	})

	It("does not count another control plane that is already being deleted toward the declared count", func() {
		r, secretName := removalReadyReconciler()
		cluster := newTestClusterWithCount(3, secretName)
		cp1 := newTestControlPlane(cluster.Name, "13")
		cp2 := newTestControlPlane(cluster.Name, "14")
		cp3 := newTestControlPlane(cluster.Name, "15")
		cp4 := newTestControlPlane(cluster.Name, "16")

		reconcileOK(r, cp1, cp2, cp3, cp4)

		// cp1 is deleted but never reconciled again, so its finalizer keeps
		// it in the API: a removal that is stuck mid-way.
		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		Expect(fetchMachine(cp1).DeletionTimestamp.IsZero()).To(BeFalse())

		// 4 listed, but only 3 are staying. Removing cp2 would leave 2.
		Expect(k8sClient.Delete(ctx, cp2)).To(Succeed())
		reconcileOK(r, cp2)
		cond := expectRemoval(fetchMachine(cp2), reasonRemovalRefused)
		Expect(cond.Message).To(ContainSubstring("declared count"))
	})

	It("defers removal while another identity holds the cluster lock, and does not touch the finalizer", func() {
		cluster := newTestClusterWithCount(1, "does-not-exist")
		cp := newTestControlPlane(cluster.Name, "13")

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		reconcileOK(r, cp)

		lock := opTestLock(cluster)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

		Expect(k8sClient.Delete(ctx, cp)).To(Succeed())
		res, err := reconcileMachine(r, cp)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval), "nothing else wakes a removal when the lock frees")

		got := fetchMachine(cp)
		expectRemoval(got, reasonClusterLockHeld)
		Expect(controllerutil.ContainsFinalizer(got, machineRemovalFinalizer)).To(BeTrue())

		Expect(lock.Release(ctx, "someone-else")).To(Succeed())
	})
})

var _ = Describe("renewWhile", func() {
	It("keeps a short-lived lock alive past its own duration, then lets it expire once stopped", func() {
		cluster := newTestClusterWithCount(1, "does-not-exist")

		// A duration shorter than the test itself: without renewal, this
		// lock would expire on its own.
		const shortDuration = 150 * time.Millisecond

		lock := clusterlock.New(k8sClient, client.ObjectKeyFromObject(cluster), shortDuration)
		Expect(lock.Acquire(ctx, controllerIdentity)).To(Succeed())

		stop := renewWhile(ctx, lock, controllerIdentity, 40*time.Millisecond)

		// Outlives its lease -- only possible if renewWhile's ticker actually called Renew repeatedly.
		expectRenewedPastLease(client.ObjectKeyFromObject(cluster), "renewWhile must renew the lock past its lease")
		Expect(lock.Acquire(ctx, "someone-else")).To(HaveOccurred())

		stop()

		// Nothing renews it anymore, so it expires and transfers.
		Eventually(func() error {
			return lock.Acquire(ctx, "someone-else")
		}, 2*time.Second, shortDuration).Should(Succeed())
	})
})

// fetchCluster fetches cluster fresh, for checking its Transitioning
// condition after a reconcile.
func fetchCluster(cluster *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCluster {
	var got ichorv1alpha1.TalosCluster
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &got)).To(Succeed())

	return &got
}

var _ = Describe("TalosCluster transitioning status", func() {
	It("reports a stable (odd) control-plane count as not transitioning", func() {
		cluster := newTestClusterWithCount(3, "does-not-exist")
		cp1 := newTestControlPlane(cluster.Name, "20")
		cp2 := newTestControlPlane(cluster.Name, "21")
		cp3 := newTestControlPlane(cluster.Name, "22")

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			// First reconcile only adds the removal finalizer and returns;
			// the transitioning update happens on the pass after that,
			// once the machine reconcile proceeds past the finalizer-add
			// branch.
			reconcileOK(r, cp)
			reconcileOK(r, cp)
		}

		cond := apimeta.FindStatusCondition(fetchCluster(cluster).Status.Conditions, conditionTransitioning)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonStableControlPlaneCount))
	})

	It("reports an even control-plane count as transitioning, once a replacement is approved", func() {
		cluster := newTestClusterWithCount(3, "does-not-exist")
		cp1 := newTestControlPlane(cluster.Name, "23")
		cp2 := newTestControlPlane(cluster.Name, "24")
		cp3 := newTestControlPlane(cluster.Name, "25")

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			reconcileOK(r, cp)
			reconcileOK(r, cp)
		}

		cp4 := newTestControlPlane(cluster.Name, "26")
		reconcileOK(r, cp4)
		reconcileOK(r, cp4)

		cond := apimeta.FindStatusCondition(fetchCluster(cluster).Status.Conditions, conditionTransitioning)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonEvenControlPlaneCount))
		Expect(cond.Message).To(ContainSubstring("4"))
	})

	It("reports a reboot-requiring change while the cluster is transitioning, without taking the lock", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}

		cp1 := newTestControlPlane(cluster.Name, "27")
		cp2 := newTestControlPlane(cluster.Name, "28")
		cp3 := newTestControlPlane(cluster.Name, "29")
		cp4 := newTestControlPlane(cluster.Name, "30")
		reconcileOK(r, cp1, cp2, cp3, cp4)

		worker := newTestMachine(cluster.Name, true)
		current := renderWithConfig(cluster, worker, bundle, layerCluster, nil, minimalConfigSkeleton())

		newTestPatch(cluster.Name, layerCluster, nil, proxyDisabledConfig())

		fake := &fakeTalosClient{current: current}
		r.TalosClient = clientFactory(fake)

		reconcileOK(r, worker)
		Expect(fake.applyCalls).To(BeEmpty())

		expectReady(fetchMachine(worker), metav1.ConditionFalse, reasonRebootRequired)

		expectLockFree(cluster, "refuseReboot never takes the cluster lock")
	})
})
