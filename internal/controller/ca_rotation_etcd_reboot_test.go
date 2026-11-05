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
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const rotCPNode = "rot-cp1-node"

// ADR 0120, hardware amendment: Talos v1.14.5 has no restart operation for the etcd service and does not
// restart it on a configuration change, so each pass is followed by a rolling reboot of the control planes.
var _ = Describe("TalosOperation ca-rotation etcd roll by reboot", func() {
	It("reboots nothing while a member is unhealthy, and goes on by itself once it is healthy", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.world.afterRestart = func(node string) {
			if len(f.world.restarts) == 1 {
				f.world.etcdUnhealthy[node] = true
			}
		}

		op := f.newOp()

		for range 12 {
			op = f.step(op)
		}

		Expect(f.world.restarts).To(HaveLen(1), "no second control plane is rebooted while the first is not healthy")
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "waiting for a rebooted node is not a failure")
		Expect(op.Status.CARotation.EtcdRebooting).To(Equal(f.controlPlaneNames()[0]))

		cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation)
		Expect(cond.Reason).To(Equal(reasonCARotationRebootingEtcd))
		Expect(cond.Message).To(ContainSubstring("not healthy"))

		f.world.etcdUnhealthy = map[string]bool{}

		f.runUntil(op, completed)
		Expect(f.world.restarts).To(HaveLen(9))
	})

	It("pauses a reboot that has not finished within the node action timeout", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.world.afterRestart = func(node string) { f.world.etcdUnhealthy[node] = true }

		op := f.newOp()
		for range 8 {
			op = f.step(op)
		}

		long := metav1.NewTime(time.Now().Add(-2 * nodeActionTimeout))
		op.Status.CARotation.EtcdRestartedAt = &long
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		op = f.step(op)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Reason).To(Equal(reasonCARotationBlocked))
	})

	It("waits for a rebooted node to answer again before it goes on, and reboots no second one meanwhile", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		first := f.controlPlaneNames()[0]
		f.world.afterRestart = func(node string) {
			if node == first && len(f.world.restarts) == 1 {
				f.world.nodes[node].down = true
			}
		}

		op := f.newOp()
		for range 12 {
			op = f.step(op)
		}

		Expect(f.world.restarts).To(HaveLen(1))
		Expect(op.Status.CARotation.EtcdRebooting).To(Equal(first))
		Expect(op.Status.CARotation.EtcdRebootBootID).To(Equal("boot-0"))

		f.world.nodes[first].down = false

		op, _ = f.runUntil(op, completed)
		Expect(f.world.restarts).To(HaveLen(9))
		Expect(op.Status.CARotation.EtcdRebooting).To(BeEmpty())
	})

	It("does not count a reboot as done while the node reports the boot it was asked to leave, and asks again after a while", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		first := f.controlPlaneNames()[0]
		f.world.frozenBoot[first] = true

		op := f.newOp()
		for range 12 {
			op = f.step(op)
		}

		Expect(f.world.restarts).To(HaveLen(1), "one request, then waiting")
		Expect(op.Status.CARotation.EtcdRestarted).To(BeEmpty())

		// A controller killed between recording the request and sending it would wait for ever: past
		// etcdRebootRetry it asks again.
		old := metav1.NewTime(time.Now().Add(-2 * etcdRebootRetry))
		op.Status.CARotation.EtcdRestartedAt = &old
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		f.world.frozenBoot[first] = false

		f.runUntil(op, completed)
		Expect(f.world.restarts).To(HaveLen(10), "the reboot of the first control plane was requested twice")
		Expect(f.world.restarts[1].node).To(Equal(first))
	})

	It("hands leadership over before it reboots the leader, and holds the roll while a Node is not Ready", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		leader := f.controlPlaneNames()[1]
		f.world.leader[leader] = true
		f.world.notReady = []string{"some-node"}

		op := f.newOp()
		for range 6 {
			op = f.step(op)
		}

		Expect(f.world.calls).To(BeEmpty(), "a Node that is not Ready holds the first reboot")

		f.world.notReady = nil
		f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

		i := slices.Index(f.world.calls, "forfeit:"+leader)
		Expect(i).To(BeNumerically(">=", 0))
		Expect(f.world.calls[i+1]).To(Equal("reboot:" + leader))
	})

	It("cordons and drains a control plane before it reboots it and uncordons it when it is back", func() {
		f := newCARotFixtureCPs(1)
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}

		cp := f.machines[0]
		f.world.nodeNames[cp.Name] = rotCPNode

		kc := fake.NewClientset(&corev1.Node{Name: rotCPNode})
		f.kube = kc

		cordoned := false

		f.world.afterRestart = func(string) {
			node, err := kc.CoreV1().Nodes().Get(ctx, rotCPNode, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			cordoned = node.Spec.Unschedulable
		}

		f.runUntil(f.newOp(), completed)
		Expect(f.world.restarts).To(HaveLen(3))
		Expect(cordoned).To(BeTrue(), "the node is cordoned when it is rebooted")

		node, err := kc.CoreV1().Nodes().Get(ctx, rotCPNode, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(node.Spec.Unschedulable).To(BeFalse(), "and schedulable again afterwards")
	})
})
