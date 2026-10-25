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
	"fmt"
	"reflect"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// setHostname patches m.Spec.Hostname to hostname -- etcd's own member list
// reports a Node's hostname, which is what removeEtcdMember matches
// against, and newTestControlPlane leaves it unset.
func setHostname(m *ichorv1alpha1.TalosMachine, hostname string) {
	m.Spec.Hostname = hostname
	Expect(k8sClient.Update(ctx, m)).To(Succeed())
}

// perMachineClientFactory returns a TalosClient factory keyed by
// TalosMachine name, for tests where the departing machine's own client
// and a surviving sibling's client need to behave differently (removal.go
// dials one, then possibly the other). A name with no entry in byName
// simulates "cannot be dialed at all" -- the forceRemove case.
func perMachineClientFactory(byName map[string]*fakeTalosClient) func(
	context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle,
) (TalosClient, error) {
	return func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
		fake, ok := byName[m.Name]
		if !ok {
			return nil, fmt.Errorf("fake dial refused for %q", m.Name)
		}

		return fake, nil
	}
}

var _ = Describe("removeEtcdMember", func() {
	It("removes the departing machine's own etcd member via EtcdLeaveCluster when it can be dialed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "e1")
		cp2 := newTestControlPlane(cluster.Name, "e2")
		setHostname(cp1, "node-e1")

		self := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-e1"}}}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{cp1.Name: self}),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		// 2 -> 1 with declared count 1: quorum of 2 is 2, which 1 fails --
		// use a 3rd so this removal actually passes its checks.
		cp3 := newTestControlPlane(cluster.Name, "e3")
		_, err := reconcileMachine(r, cp3)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		_, err = reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(self.etcdLeaveCalls).To(Equal(1))
		Expect(self.etcdRemoveIDs).To(BeEmpty())

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the finalizer must be cleared once EtcdLeaveCluster succeeds")
	})

	It("falls back to a surviving sibling's EtcdRemoveMemberByID when the departing machine cannot be dialed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "e4")
		cp2 := newTestControlPlane(cluster.Name, "e5")
		cp3 := newTestControlPlane(cluster.Name, "e6")
		setHostname(cp1, "node-e4")

		survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 42, Hostname: "node-e4"}}}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			// cp1 (the departing machine) has no entry: it cannot be dialed at
			// all, as if the node were powered off.
			TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{
				cp2.Name: survivor, cp3.Name: survivor,
			}),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{42}))
		Expect(survivor.etcdLeaveCalls).To(Equal(0))

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("is idempotent: a departing machine already absent from etcd needs no further Talos call", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "e7")
		cp2 := newTestControlPlane(cluster.Name, "e8")
		cp3 := newTestControlPlane(cluster.Name, "e9")
		setHostname(cp1, "node-e7")

		// cp1's own client reports an etcd member list that no longer
		// contains "node-e7" -- the resumption case: killed after a
		// previous reconcile's EtcdLeaveCluster already succeeded, before
		// the finalizer was cleared.
		self := &fakeTalosClient{etcdMembers: nil}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{cp1.Name: self}),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		Expect(self.etcdLeaveCalls).To(Equal(0), "already gone: leaving again is not idempotency, it's a wasted call")

		var gone ichorv1alpha1.TalosMachine
		err = k8sClient.Get(ctx, machineKeyOf(cp1), &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("holds the finalizer and does not clear it when no survivor can be reached", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "e10")
		cp2 := newTestControlPlane(cluster.Name, "e11")
		cp3 := newTestControlPlane(cluster.Name, "e12")
		setHostname(cp1, "node-e10")

		// Nobody in the map: cp1 cannot be dialed, and neither can any
		// sibling removeEtcdMemberFromSurvivor would try.
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{}),
		}
		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		_, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(cp1)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
		Expect(cond.Reason).To(Equal(reasonEtcdMemberRemoveFail))
		Expect(got.Finalizers).To(ContainElement(machineRemovalFinalizer), "a failed etcd removal must not clear the finalizer")
	})
})

// machineKeyOf is a name/namespace key to re-Get m after a removal that
// should have let the object actually disappear.
func machineKeyOf(m *ichorv1alpha1.TalosMachine) types.NamespacedName {
	return types.NamespacedName{Namespace: m.Namespace, Name: m.Name}
}

// TestTalosClientHasNoResetMethod guards architecture.md's "Nodes" section
// item about Reset being fire-and-forget and needing the request recorded
// before a crash: ADR 0032 keeps Reset out of this interface entirely, so
// that failure mode cannot be stepped into by construction. This fails
// loudly if a future change adds any method whose name contains "Reset",
// which would need that fire-and-forget handling all over again.
func TestTalosClientHasNoResetMethod(t *testing.T) {
	typ := reflect.TypeFor[TalosClient]()

	for m := range typ.Methods() {
		if strings.Contains(strings.ToLower(m.Name), "reset") {
			t.Fatalf("TalosClient gained a Reset-shaped method (%s): Reset is a separate unit "+
				"(ADR 0032) precisely because it is fire-and-forget and needs its request recorded "+
				"before a crash, unlike everything else on this interface -- that handling has to come "+
				"back before this method does, not as a side effect of adding it here", m.Name)
		}
	}
}
