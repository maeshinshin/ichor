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
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The node at an address that machine A had can be machine B, which trusts the same CA. Nothing that
// wipes, upgrades or reconfigures A, or takes it out of etcd, may go to B.
var _ = Describe("a destructive call to a node that is not the machine it is named for", func() {
	const (
		elsewhere = "uuid-of-another-machine"
		bNode     = "node-of-b"
		wipedNode = "node-id2"
	)

	upgradeCase := 0

	// Each wrong answer a node can give: another machine's UUID, no UUID, no answer to the question.
	wrong := []TableEntry{
		Entry("answers as another machine", HardwareIdentity{UUID: elsewhere}, error(nil)),
		Entry("reports no UUID", HardwareIdentity{Serial: "s1"}, error(nil)),
		Entry("does not answer the identity question", HardwareIdentity{}, errors.New("identity unavailable")),
	}

	DescribeTable("a removal does not make it leave etcd, and takes the member out through a survivor",
		func(id HardwareIdentity, idErr error) {
			self := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 7, Hostname: "node-id1"}}, identity: id, identityErr: idErr}
			survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 7, Hostname: "node-id1"}}}

			cp1, err := removeFirstOfThree("node-id1", nil, map[int]*fakeTalosClient{0: self, 1: survivor, 2: survivor})
			Expect(err).NotTo(HaveOccurred())
			Expect(self.etcdLeaveCalls).To(BeZero(), "the node there was not asked to leave")
			Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{7}))
			Expect(machineGone(cp1)).To(BeTrue())
		}, wrong)

	DescribeTable("a removal does not take the Kubernetes Node and etcd member of whoever answers as the machine",
		func(id HardwareIdentity, idErr error) {
			self := &fakeTalosClient{nodeName: bNode, etcdMembers: []EtcdMember{{ID: 9, Hostname: bNode}}, identity: id, identityErr: idErr}
			survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 9, Hostname: bNode}}}

			cp1, err := removeFirstOfThree("", nil, map[int]*fakeTalosClient{0: self, 1: survivor, 2: survivor})
			Expect(err).To(HaveOccurred(), "an error, so the removal is retried: the node may only be rebooting")
			Expect(self.etcdLeaveCalls).To(BeZero())
			Expect(survivor.etcdRemoveIDs).To(BeEmpty())
			expectRemoval(fetchMachine(cp1), reasonNodeNameUnresolved)
			Expect(machineGone(cp1)).To(BeFalse())
		}, wrong)

	DescribeTable("a removal that asks for a reset does not wipe it, and holds while its Kubernetes Node is up",
		func(id HardwareIdentity, idErr error) {
			cps := newThreeControlPlanes()
			setHostname(cps[0], wipedNode)
			newTestNode(wipedNode, true)
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, &corev1.Node{Name: wipedNode}) })

			self := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 3, Hostname: wipedNode}}, identity: id, identityErr: idErr}
			survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 3, Hostname: wipedNode}}}
			r := &TalosMachineReconciler{
				Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, Recorder: record.NewFakeRecorder(10),
				TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{cps[0].Name: self, cps[1].Name: survivor, cps[2].Name: survivor}),
			}
			reconcileEach(r, cps)

			got := fetchMachine(cps[0])
			got.Annotations = map[string]string{resetOnDeleteAnnotation: resetOnDeleteValue}
			Expect(k8sClient.Update(ctx, got)).To(Succeed())
			Expect(k8sClient.Delete(ctx, got)).To(Succeed())

			reconcileOK(r, cps[0])

			Expect(self.resetCalls).To(BeZero(), "the node there was not wiped")
			Expect(self.etcdLeaveCalls).To(BeZero())
			cond := expectRemoval(fetchMachine(cps[0]), reasonResetFailed)
			Expect(cond.Message).To(ContainSubstring("nothing was sent"))
			Expect(machineGone(cps[0])).To(BeFalse())
		}, wrong)

	It("lets a removal whose own Kubernetes Node is gone go on unwiped, without wiping what answers at its address", func() {
		cps := newThreeControlPlanes()
		setHostname(cps[0], "node-id3")

		self := &fakeTalosClient{identity: HardwareIdentity{UUID: elsewhere}}
		survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 4, Hostname: "node-id3"}}}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, Recorder: record.NewFakeRecorder(10),
			TalosClient: perMachineClientFactory(map[string]*fakeTalosClient{cps[0].Name: self, cps[1].Name: survivor, cps[2].Name: survivor}),
		}
		reconcileEach(r, cps)

		got := fetchMachine(cps[0])
		got.Annotations = map[string]string{resetOnDeleteAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(k8sClient.Delete(ctx, got)).To(Succeed())

		reconcileOK(r, cps[0])

		Expect(self.resetCalls).To(BeZero())
		Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{4}))
		Expect(machineGone(cps[0])).To(BeTrue())
	})

	DescribeTable("a configuration rollout sends it nothing",
		func(id HardwareIdentity, idErr error) {
			f := newRolloutFixture()
			m := newTestMachine(f.cluster.Name, true)

			fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore, identity: id, identityErr: idErr}

			done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
			Expect(err).To(MatchError(ContainSubstring("nothing was sent")))
			Expect(done).To(BeFalse())
			Expect(fake.stagedCalls).To(BeEmpty())
			Expect(fake.rebootCalls).To(BeZero())
		}, wrong)

	DescribeTable("a Talos upgrade sends it nothing",
		func(id HardwareIdentity, idErr error) {
			upgradeCase++

			cluster, cp := newUpgradeNode(strconv.Itoa(200+upgradeCase), true)
			fake := &fakeTalosClient{version: "v0.0.1", bootID: testBootIDBefore, identity: id, identityErr: idErr}

			done, err := newUpgradeAdvancer(fake, nil).Advance(ctx, cluster, fetchMachine(cp), nil)
			Expect(err).To(MatchError(ContainSubstring("nothing was sent")))
			Expect(done).To(BeFalse())
			Expect(fake.upgradeCalls).To(BeEmpty())
		}, wrong)

	DescribeTable("a node action is refused before it reaches it",
		func(id HardwareIdentity, idErr error) {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			f.fake(hostW1).identity, f.fake(hostW1).identityErr = id, idErr

			got := f.reconcile(f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("nothing was sent"))
			Expect(f.fake(hostW1).rebootCalls).To(BeZero())
		}, wrong)
})
