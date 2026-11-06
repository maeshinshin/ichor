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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0135: a removal that does not reset powers the machine off and deletes its Kubernetes Node.
var _ = Describe("removal: shutdown and delete the Kubernetes Node (ADR 0135)", func() {
	nodeGone := func(name string) bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &corev1.Node{}))
	}

	setReady := func(name string, ready bool) {
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &n)).To(Succeed())

		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}

		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())
	}

	newNode := func(name string, ready bool) {
		newTestNode(name, ready)
		n := &corev1.Node{}
		n.Name = name
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })
	}

	// cpRemoval makes three reconciled control planes and deletes the first.
	cpRemoval := func(fake *fakeTalosClient) (*TalosMachineReconciler, *ichorv1alpha1.TalosMachine, *record.FakeRecorder) {
		cps := newThreeControlPlanes()
		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder,
		}
		reconcileEach(r, cps)

		Expect(k8sClient.Delete(ctx, cps[0])).To(Succeed())

		return r, cps[0], recorder
	}

	It("shuts a control plane down, waits for its Node to go NotReady, then deletes the Node and the machine", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-cp-wait"}
		newNode("rmnode-cp-wait", true)
		r, cp, recorder := cpRemoval(fake)

		res, err := reconcileMachine(r, cp)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval))
		Expect(fake.shutdownCalls).To(Equal([]bool{true}))
		Expect(nodeGone("rmnode-cp-wait")).To(BeFalse(), "a Ready Node is not deleted while the machine may still run")
		Expect(machineGone(cp)).To(BeFalse())

		setReady("rmnode-cp-wait", false)
		reconcileOK(r, cp)

		Expect(fake.shutdownCalls).To(HaveLen(1), "the shutdown is sent once")
		Expect(nodeGone("rmnode-cp-wait")).To(BeTrue())
		Expect(machineGone(cp)).To(BeTrue())
		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(reasonNodeStillHasClusterCredentials), ContainSubstring("powered off"), ContainSubstring("was deleted"))))
	})

	It("deletes the Node of a worker the same way", func() {
		cps := newThreeControlPlanes()
		fake := &fakeTalosClient{nodeName: "rmnode-worker"}
		newNode("rmnode-worker", false)

		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder,
		}

		w := newTestMachine(cps[0].Spec.ClusterRef.Name, true)
		controllerutil.AddFinalizer(w, machineRemovalFinalizer)
		Expect(k8sClient.Update(ctx, w)).To(Succeed())
		Expect(k8sClient.Delete(ctx, w)).To(Succeed())

		reconcileOK(r, w)

		Expect(fake.shutdownCalls).To(Equal([]bool{true}))
		Expect(fake.etcdLeaveCalls).To(Equal(0), "a worker was never an etcd member")
		Expect(nodeGone("rmnode-worker")).To(BeTrue())
		Expect(machineGone(w)).To(BeTrue())
		Eventually(recorder.Events).Should(Receive(ContainSubstring("worker")))
	})

	It("deletes the Node of a node that cannot be told to shut down, and says so", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-unreachable", shutdownErr: errors.New("connection refused")}
		newNode("rmnode-unreachable", false)
		r, cp, recorder := cpRemoval(fake)

		reconcileOK(r, cp)

		Expect(nodeGone("rmnode-unreachable")).To(BeTrue())
		Expect(machineGone(cp)).To(BeTrue())
		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring("shutdown could not be sent"), ContainSubstring("connection refused"))))
	})

	It("does not delete a Node that is still Ready after the wait, and warns with the manual step", func() {
		old := nodeDownWait
		nodeDownWait = time.Nanosecond
		DeferCleanup(func() { nodeDownWait = old })

		fake := &fakeTalosClient{nodeName: "rmnode-stale", shutdownErr: errors.New("refused")}
		newNode("rmnode-stale", true)
		r, cp, recorder := cpRemoval(fake)

		reconcileOK(r, cp)

		Expect(nodeGone("rmnode-stale")).To(BeFalse())
		Expect(machineGone(cp)).To(BeTrue(), "the removal still finishes")
		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(reasonNodeStillHasClusterCredentials), ContainSubstring("kubectl delete node rmnode-stale"))))
	})

	It("does not shut down or delete the Node of a removal that resets", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-reset"}
		newNode("rmnode-reset", true)

		cps := newThreeControlPlanes()
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: record.NewFakeRecorder(10),
		}
		reconcileEach(r, cps)

		optIntoReset(cps[0])
		reconcileOK(r, cps[0])

		Expect(fake.resetCalls).To(Equal(1))
		Expect(fake.shutdownCalls).To(BeEmpty())
		Expect(nodeGone("rmnode-reset")).To(BeFalse())
		Expect(machineGone(cps[0])).To(BeTrue())
	})

	It("deletes nothing when the machine has no Kubernetes Node, and still shuts it down", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-none"}
		r, cp, _ := cpRemoval(fake)

		reconcileOK(r, cp)

		Expect(fake.shutdownCalls).To(Equal([]bool{true}))
		Expect(machineGone(cp)).To(BeTrue())
	})
})
