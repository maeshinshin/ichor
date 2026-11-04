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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Two control-plane machines of one cluster must not change etcd membership at the same time: a
// join and a removal each take the cluster lock under the machine's own identity, so a second one
// that arrives while the first holds it waits. Each
// test lets the first machine's reconcile pause inside its locked step (a hook on the fake node
// API) and runs the second machine's reconcile there, the way a second worker would.
var _ = Describe("two control-plane TalosMachines of one cluster at a step that takes the cluster lock", func() {
	var (
		cluster *ichorv1alpha1.TalosCluster
		talos   *fakeTalosClient
		maint   map[string]*fakeMaintenanceClient
		r       *TalosMachineReconciler
	)

	BeforeEach(func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster = newTestClusterWithCount(3, secret.Name)

		wipe := false
		cfg := minimalConfigSkeleton()
		cfg.MachineConfig.MachineType = roleControlPlane
		cfg.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/vda", InstallWipe: &wipe}
		newTestPatch(cluster.Name, layerCluster, nil, cfg)

		talos = &fakeTalosClient{currentErr: errUnauthenticated, nodeName: "lock-test-node"}
		maint = map[string]*fakeMaintenanceClient{}
		r = &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(talos),
			MaintenanceClient: func(_ context.Context, endpoint string) (MaintenanceClient, error) { return maint[endpoint], nil },
		}
	})

	// controlPlane is an approved control plane that has had its first reconcile (the removal finalizer).
	controlPlane := func(suffix string) *ichorv1alpha1.TalosMachine {
		m := newTestControlPlane(cluster.Name, suffix)
		maint[m.Spec.Endpoint] = &fakeMaintenanceClient{identity: HardwareIdentity{UUID: m.Spec.Identity.UUID}}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		return m
	}

	// once runs fn the first time it is called and never again.
	once := func(fn func()) func() {
		return func() {
			f := fn
			fn = func() {}
			f()
		}
	}

	expectWaitsFor := func(m, holder *ichorv1alpha1.TalosMachine, reason string, condition string) {
		cond := apimeta.FindStatusCondition(fetchMachine(m).Status.Conditions, condition)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reason), "a machine that arrives while another holds the cluster lock waits")
		Expect(cond.Message).To(ContainSubstring(holder.Name), "and names the machine that holds it")
	}

	It("makes a second join wait, naming the first, and lets it through once the first has stored its configuration", func() {
		a, b := controlPlane("41"), controlPlane("42")
		maintA, maintB := maint[a.Spec.Endpoint], maint[b.Spec.Endpoint]

		maintA.applyHook = once(func() {
			_, err := reconcileMachine(r, b)
			Expect(err).NotTo(HaveOccurred())
			Expect(maintB.applyCalls).To(BeEmpty(), "a second join must not apply while the first holds the cluster lock")
			expectWaitsFor(b, a, reasonClusterLockHeld, conditionReady)
		})

		_, err := reconcileMachine(r, a)
		Expect(err).NotTo(HaveOccurred())
		Expect(maintA.applyCalls).To(HaveLen(1))
		Expect(fetchMachine(a).Status.ConfigStoredAt).NotTo(BeNil())

		_, held, err := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0).Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "the first join released the lock; the second's own release did not free it early")

		_, err = reconcileMachine(r, b)
		Expect(err).NotTo(HaveOccurred())
		Expect(maintB.applyCalls).To(HaveLen(1))
	})

	It("makes a second removal wait, naming the first, and leaves its finalizer", func() {
		a, b := controlPlane("43"), controlPlane("44")
		controlPlane("45")
		controlPlane("46")

		Expect(k8sClient.Delete(ctx, a)).To(Succeed())

		ran := false
		talos.etcdListHook = once(func() {
			ran = true

			Expect(k8sClient.Delete(ctx, b)).To(Succeed())

			_, err := reconcileMachine(r, b)
			Expect(err).NotTo(HaveOccurred())
			expectWaitsFor(b, a, reasonClusterLockHeld, conditionRemoval)
			Expect(controllerutil.ContainsFinalizer(fetchMachine(b), machineRemovalFinalizer)).To(BeTrue())
		})

		_, _ = reconcileMachine(r, a)
		Expect(ran).To(BeTrue(), "the first removal reached its etcd step")
	})

	It("makes a join wait for a removal that is in progress", func() {
		a := controlPlane("47")
		controlPlane("48")
		controlPlane("49")
		joiner := controlPlane("50")

		Expect(k8sClient.Delete(ctx, a)).To(Succeed())

		ran := false
		talos.etcdListHook = once(func() {
			ran = true

			// The join's reconcile after the finalizer pass; the first one ran in controlPlane.
			_, err := reconcileMachine(r, joiner)
			Expect(err).NotTo(HaveOccurred())
			Expect(maint[joiner.Spec.Endpoint].applyCalls).To(BeEmpty(), "a join must not apply while a removal holds the cluster lock")
			expectWaitsFor(joiner, a, reasonClusterLockHeld, conditionReady)
		})

		_, _ = reconcileMachine(r, a)
		Expect(ran).To(BeTrue(), "the removal reached its etcd step")
	})

	It("makes a removal wait for a join that is in progress, and leaves its finalizer", func() {
		a := controlPlane("51")
		controlPlane("52")
		controlPlane("53")
		joiner := controlPlane("54")

		Expect(k8sClient.Delete(ctx, a)).To(Succeed())

		ran := false
		maint[joiner.Spec.Endpoint].applyHook = once(func() {
			ran = true

			_, err := reconcileMachine(r, a)
			Expect(err).NotTo(HaveOccurred())
			Expect(talos.etcdRemoveIDs).To(BeEmpty())

			got := &ichorv1alpha1.TalosMachine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: a.Namespace, Name: a.Name}, got)).To(Succeed(),
				"a removal must not complete while a join holds the cluster lock")
			Expect(controllerutil.ContainsFinalizer(got, machineRemovalFinalizer)).To(BeTrue())
			expectWaitsFor(a, joiner, reasonClusterLockHeld, conditionRemoval)
		})

		_, err := reconcileMachine(r, joiner)
		Expect(err).NotTo(HaveOccurred())
		Expect(ran).To(BeTrue(), "the join reached its apply")
	})

	Describe("a lease whose holder will never renew it", func() {
		var lock *clusterlock.Lock

		BeforeEach(func() {
			lock = clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		})

		// join reconciles joiner once (its locked step) and reports whether it applied.
		join := func(joiner *ichorv1alpha1.TalosMachine) bool {
			_, err := reconcileMachine(r, joiner)
			Expect(err).NotTo(HaveOccurred())

			return len(maint[joiner.Spec.Endpoint].applyCalls) > 0
		}

		It("is taken over from a machine that is gone, and from one whose configuration is already stored", func() {
			gone := newTestControlPlane(cluster.Name, "61") // never reconciled: no finalizer, so deleting removes it
			Expect(lock.Acquire(ctx, machineLockIdentity(gone))).To(Succeed())
			Expect(k8sClient.Delete(ctx, gone)).To(Succeed())

			Expect(join(controlPlane("62"))).To(BeTrue(), "the lease of a deleted machine is not waited for")
			Expect(lock.Release(ctx, machineLockIdentity(gone))).To(Succeed())

			stored := newTestControlPlane(cluster.Name, "63")
			got := fetchMachine(stored)
			now := metav1.Now()
			got.Status.ConfigStoredAt = &now
			Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
			Expect(lock.Acquire(ctx, machineLockIdentity(stored))).To(Succeed())

			Expect(join(controlPlane("64"))).To(BeTrue(), "a join that stored its configuration has nothing left under the lock")
		})

		It("is taken over from a TalosOperation that is gone or completed, and kept by one that is running", func() {
			gone := newOpTestOperation(cluster.Name)
			Expect(lock.Acquire(ctx, operationLockIdentity(gone))).To(Succeed())
			Expect(k8sClient.Delete(ctx, gone)).To(Succeed())

			Expect(join(controlPlane("65"))).To(BeTrue(), "the lease of a deleted operation is not waited for")

			running := newOpTestOperation(cluster.Name)
			Expect(lock.Acquire(ctx, operationLockIdentity(running))).To(Succeed())

			waiting := controlPlane("66")
			Expect(join(waiting)).To(BeFalse(), "an operation that has not finished may still be working")
			cond := readyCondition(fetchMachine(waiting))
			Expect(cond.Reason).To(Equal(reasonClusterLockHeld))
			Expect(cond.Message).To(ContainSubstring("TalosOperation"))
			Expect(cond.Message).To(ContainSubstring(running.Name))

			got := fetchOperation(running)
			got.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
			Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

			Expect(join(waiting)).To(BeTrue(), "a completed operation holds nothing")
		})

		It("is kept by a machine whose removal is still running", func() {
			removing := controlPlane("67")
			Expect(lock.Acquire(ctx, machineLockIdentity(removing))).To(Succeed())
			Expect(k8sClient.Delete(ctx, removing)).To(Succeed())

			waiting := controlPlane("68")
			Expect(join(waiting)).To(BeFalse(), "the removal is the holder's locked step and runs until its finalizer goes")
			Expect(readyCondition(fetchMachine(waiting)).Message).To(ContainSubstring(removing.Name))
		})

		It("is held by the Cilium Helm Job: a control plane join waits for it", func() {
			Expect(lock.Acquire(ctx, cniHelmLockIdentity)).To(Succeed())
			DeferCleanup(func() { Expect(lock.Release(ctx, cniHelmLockIdentity)).To(Succeed()) })

			cp := controlPlane("71")
			Expect(join(cp)).To(BeFalse())
			Expect(readyCondition(fetchMachine(cp)).Reason).To(Equal(reasonClusterLockHeld))
			Expect(readyCondition(fetchMachine(cp)).Message).To(ContainSubstring(cniHelmLockIdentity))
		})

		It("is left by the previous build under the shared identity: a join waits, and it ages out", func() {
			legacy := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, time.Second)
			Expect(legacy.Acquire(ctx, controllerIdentity)).To(Succeed())

			joiner := controlPlane("69")
			Expect(join(joiner)).To(BeFalse(), "nothing says the old lease is dead")
			Expect(readyCondition(fetchMachine(joiner)).Reason).To(Equal(reasonClusterLockHeld))

			Eventually(func() bool { return join(joiner) }, 5*time.Second, 200*time.Millisecond).Should(BeTrue(),
				"nothing renews it any more, so it expires on its own")
		})
	})
})
