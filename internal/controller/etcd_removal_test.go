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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

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

		return fake.dialedAs(m), nil
	}
}

// newThreeControlPlanes declares a cluster with one control plane and gives it three.
func newThreeControlPlanes() []*ichorv1alpha1.TalosMachine {
	cluster := newTestClusterWithCount(1, newTestSecret(newTestBundle("v1.14")).Name)

	return []*ichorv1alpha1.TalosMachine{
		newTestControlPlane(cluster.Name, "1"), newTestControlPlane(cluster.Name, "2"), newTestControlPlane(cluster.Name, "3"),
	}
}

// reconcileEach reconciles every machine once, which is what gives each its removal finalizer.
func reconcileEach(r *TalosMachineReconciler, ms []*ichorv1alpha1.TalosMachine) {
	GinkgoHelper()

	for _, m := range ms {
		reconcileOK(r, m)
	}
}

// removeFirstOfThree reconciles three control planes once with a reconciler whose dial answers per
// index in clients (0 is the first control plane; a missing index cannot be dialed), then deletes
// the first and reconciles it again. hostname, when set, is the first one's spec.hostname.
func removeFirstOfThree(
	hostname string, recorder record.EventRecorder, clients map[int]*fakeTalosClient,
) (*ichorv1alpha1.TalosMachine, error) {
	cps := newThreeControlPlanes()

	if hostname != "" {
		setHostname(cps[0], hostname)
	}

	byName := map[string]*fakeTalosClient{}
	for i, c := range clients {
		byName[cps[i].Name] = c
	}

	r := &TalosMachineReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, Recorder: recorder,
		TalosClient: perMachineClientFactory(byName),
	}
	reconcileEach(r, cps)

	Expect(k8sClient.Delete(ctx, cps[0])).To(Succeed())
	_, err := reconcileMachine(r, cps[0])

	return cps[0], err
}

var _ = Describe("removeEtcdMember", func() {
	It("removes the departing machine's own etcd member via EtcdLeaveCluster when it can be dialed", func() {
		self := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-e1"}}}

		cp1, err := removeFirstOfThree("node-e1", nil, map[int]*fakeTalosClient{0: self})
		Expect(err).NotTo(HaveOccurred())
		Expect(self.etcdLeaveCalls).To(Equal(1))
		Expect(self.etcdRemoveIDs).To(BeEmpty())

		Expect(machineGone(cp1)).To(BeTrue(), "the finalizer must be cleared once EtcdLeaveCluster succeeds")
	})

	It("falls back to a surviving sibling's EtcdRemoveMemberByID when the departing machine cannot be dialed", func() {
		survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 42, Hostname: "node-e4"}}}

		// The departing machine has no entry: it cannot be dialed at all, as if the node were powered off.
		cp1, err := removeFirstOfThree("node-e4", nil, map[int]*fakeTalosClient{1: survivor, 2: survivor})
		Expect(err).NotTo(HaveOccurred())
		Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{42}))
		Expect(survivor.etcdLeaveCalls).To(Equal(0))

		Expect(machineGone(cp1)).To(BeTrue())
	})

	It("is idempotent: a departing machine already absent from etcd needs no further Talos call", func() {
		// The machine's own client reports an etcd member list that no longer contains "node-e7" -- the
		// resumption case: killed after a previous reconcile's EtcdLeaveCluster already succeeded,
		// before the finalizer was cleared.
		self := &fakeTalosClient{etcdMembers: nil}

		cp1, err := removeFirstOfThree("node-e7", nil, map[int]*fakeTalosClient{0: self})
		Expect(err).NotTo(HaveOccurred())
		Expect(self.etcdLeaveCalls).To(Equal(0), "already gone: leaving again is not idempotency, it's a wasted call")

		Expect(machineGone(cp1)).To(BeTrue())
	})

	It("holds the finalizer and does not clear it when no survivor can be reached", func() {
		// Nobody can be dialed: neither the machine nor any sibling removeEtcdMemberFromSurvivor would try.
		cp1, err := removeFirstOfThree("node-e10", nil, nil)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(cp1)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
		Expect(cond.Reason).To(Equal(reasonEtcdMemberRemoveFail))
		Expect(got.Finalizers).To(ContainElement(machineRemovalFinalizer), "a failed etcd removal must not clear the finalizer")
	})
})

var _ = Describe("removal of a control plane with an empty spec.hostname", func() {
	It("resolves the node name from the node's own report and leaves etcd under that name", func() {
		self := &fakeTalosClient{nodeName: "talos-abc-def", etcdMembers: []EtcdMember{{ID: 7, Hostname: "talos-abc-def"}}}

		_, err := removeFirstOfThree("", nil, map[int]*fakeTalosClient{0: self})
		Expect(err).NotTo(HaveOccurred())
		Expect(self.etcdLeaveCalls).To(Equal(1), "the member must be found under the resolved name, not skipped")
	})

	It("refuses the removal, keeping the finalizer, when the node name cannot be resolved", func() {
		self := &fakeTalosClient{nodeNameErr: fmt.Errorf("node is down")}
		recorder := record.NewFakeRecorder(10)

		cp1, _ := removeFirstOfThree("", recorder, map[int]*fakeTalosClient{0: self})

		got := fetchMachine(cp1)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionRemoval)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNodeNameUnresolved))
		Expect(got.Finalizers).To(ContainElement(machineRemovalFinalizer))
		Expect(self.etcdLeaveCalls).To(Equal(0))
		Expect(recorder.Events).To(BeEmpty(), "no event may claim an etcd removal that did not happen")
	})
})
