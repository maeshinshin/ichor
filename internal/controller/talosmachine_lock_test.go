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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Two control-plane machines of one cluster must not change etcd membership at the same time: a
// join and a removal each take the cluster lock under the machine's own identity, so a second one
// that arrives while the first holds it waits (ADR 0026 amendment 2026-11-04, follow-up). Each
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
			Expect(len(maintB.applyCalls)).To(BeZero(), "a second join must not apply while the first holds the cluster lock")
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
			Expect(len(maint[joiner.Spec.Endpoint].applyCalls)).To(BeZero(), "a join must not apply while a removal holds the cluster lock")
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
})
