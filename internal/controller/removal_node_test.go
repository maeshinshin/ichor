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
	"fmt"
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

// A removal wipes the machine (power off or reboot) and deletes its Kubernetes Node.
var _ = Describe("removal: reset, then delete the Kubernetes Node", func() {
	nodeGone := func(name string) bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &corev1.Node{}))
	}

	newNode := func(name string, ready bool) {
		newTestNode(name, ready)

		n := &corev1.Node{}
		n.Name = name
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })
	}

	// annotate sets machine annotations the way RemoveMachine does, then deletes the machine.
	annotate := func(m *ichorv1alpha1.TalosMachine, powerOff bool) {
		got := fetchMachine(m)
		got.Annotations = map[string]string{resetOnDeleteAnnotation: resetOnDeleteValue}

		if powerOff {
			got.Annotations[resetPowerOffAnnotation] = resetOnDeleteValue
		}

		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(k8sClient.Delete(ctx, got)).To(Succeed())
	}

	setup := func(fake *fakeTalosClient) (*TalosMachineReconciler, []*ichorv1alpha1.TalosMachine, *record.FakeRecorder) {
		cps := newThreeControlPlanes()
		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder,
		}
		reconcileEach(r, cps)

		return r, cps, recorder
	}

	It("powers a control plane off after the wipe, waits for its Node to go NotReady, then deletes the Node and the machine", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-cp-off"}
		newNode("rmnode-cp-off", true)
		r, cps, _ := setup(fake)
		annotate(cps[0], true)

		res, err := reconcileMachine(r, cps[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval))
		Expect(fake.resetPowerOff).To(Equal([]bool{true}))
		Expect(nodeGone("rmnode-cp-off")).To(BeFalse(), "a Ready Node is not deleted while the machine may still run")
		Expect(machineGone(cps[0])).To(BeFalse())

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rmnode-cp-off"}, &n)).To(Succeed())
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())

		reconcileOK(r, cps[0])

		Expect(fake.resetCalls).To(Equal(1), "the reset is sent once")
		Expect(nodeGone("rmnode-cp-off")).To(BeTrue())
		Expect(machineGone(cps[0])).To(BeTrue())
	})

	for _, mode := range []string{"", ichorv1alpha1.WipeModeSystemDisk, ichorv1alpha1.WipeModeAll} {
		It("passes the wipe mode "+fmt.Sprintf("%q", mode)+" of a remove to Talos", func() {
			fake := &fakeTalosClient{nodeName: "rmnode-wipe"}
			r, cps, _ := setup(fake)

			got := fetchMachine(cps[0])
			got.Annotations = map[string]string{resetOnDeleteAnnotation: resetOnDeleteValue}
			if mode != "" {
				got.Annotations[resetWipeModeAnnotation] = mode
			}
			Expect(k8sClient.Update(ctx, got)).To(Succeed())
			Expect(k8sClient.Delete(ctx, got)).To(Succeed())

			_, err := reconcileMachine(r, cps[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(fake.resetModes).To(Equal([]string{mode}))
		})
	}

	It("reboots into maintenance mode when asked, and deletes the Node of a worker", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-worker"}
		newNode("rmnode-worker", false)
		r, cps, _ := setup(fake)

		w := newTestMachine(cps[0].Spec.ClusterRef.Name, true)
		controllerutil.AddFinalizer(w, machineRemovalFinalizer)
		Expect(k8sClient.Update(ctx, w)).To(Succeed())
		annotate(w, false)

		reconcileOK(r, w)

		Expect(fake.resetPowerOff).To(Equal([]bool{false}))
		Expect(fake.etcdLeaveCalls).To(Equal(0), "a worker was never an etcd member")
		Expect(nodeGone("rmnode-worker")).To(BeTrue())
		Expect(machineGone(w)).To(BeTrue())
	})

	It("removes a node that does not answer, deletes its NotReady Node, and says it was not wiped", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-dead", resetErr: errors.New("connection refused")}
		newNode("rmnode-dead", false)
		r, cps, recorder := setup(fake)
		annotate(cps[0], true)

		reconcileOK(r, cps[0])

		Expect(nodeGone("rmnode-dead")).To(BeTrue())
		Expect(machineGone(cps[0])).To(BeTrue())
		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(reasonNodeStillHasClusterCredentials), ContainSubstring("not wiped"), ContainSubstring("connection refused"))))
	})

	It("holds the removal when a node that is still Ready cannot be reset", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-up", resetErr: errors.New("busy")}
		newNode("rmnode-up", true)
		r, cps, _ := setup(fake)
		annotate(cps[0], true)

		_, err := reconcileMachine(r, cps[0])
		Expect(err).NotTo(HaveOccurred())

		Expect(nodeGone("rmnode-up")).To(BeFalse())
		Expect(machineGone(cps[0])).To(BeFalse())
	})

	It("does not delete a Node that is still Ready after the wait, and warns with the manual step", func() {
		old := nodeDownWait
		nodeDownWait = time.Nanosecond
		DeferCleanup(func() { nodeDownWait = old })

		fake := &fakeTalosClient{nodeName: "rmnode-stale"}
		newNode("rmnode-stale", true)
		r, cps, recorder := setup(fake)
		annotate(cps[0], true)

		reconcileOK(r, cps[0]) // sends the reset and waits
		reconcileOK(r, cps[0]) // the wait is over

		Expect(nodeGone("rmnode-stale")).To(BeFalse())
		Expect(machineGone(cps[0])).To(BeTrue(), "the removal still finishes")
		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(reasonNodeNotDeleted), ContainSubstring("kubectl delete node rmnode-stale"))))
	})

	It("does not reset or touch the Node of a removal that did not ask for a reset", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-plain"}
		newNode("rmnode-plain", true)
		r, cps, _ := setup(fake)

		Expect(k8sClient.Delete(ctx, cps[0])).To(Succeed())
		reconcileOK(r, cps[0])

		Expect(fake.resetCalls).To(Equal(0))
		Expect(nodeGone("rmnode-plain")).To(BeFalse())
		Expect(machineGone(cps[0])).To(BeTrue())
	})

	It("does not finish a reset the machine does not confirm in maintenance mode, though its Node went NotReady", func() {
		fake := &fakeTalosClient{nodeName: "rmnode-unconfirmed"}
		newNode("rmnode-unconfirmed", true)
		r, cps, recorder := setup(fake)
		annotate(cps[0], false)

		old := resetMaintenanceProbe
		resetMaintenanceProbe = func(context.Context, string, string) bool { return false }
		DeferCleanup(func() { resetMaintenanceProbe = old })

		_, err := reconcileMachine(r, cps[0]) // the reset is sent; the Node is still Ready
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.resetCalls).To(Equal(1))

		// A machine that rebooted with its disk intact also goes NotReady: that is not a wipe.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "rmnode-unconfirmed"}, &n)).To(Succeed())
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())

		_, err = reconcileMachine(r, cps[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(machineGone(cps[0])).To(BeFalse(), "the removal waits for the machine to confirm the wipe")
		Expect(nodeGone("rmnode-unconfirmed")).To(BeFalse())

		old2 := nodeDownWait
		nodeDownWait = time.Nanosecond
		DeferCleanup(func() { nodeDownWait = old2 })

		reconcileOK(r, cps[0]) // the wait is over: the removal finishes and says the wipe is not confirmed

		Expect(machineGone(cps[0])).To(BeTrue())
		Eventually(recorder.Events).Should(Receive(ContainSubstring("not confirmed")))
	})
})
