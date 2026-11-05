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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const drainRetryNode = "drain-retry-node"

var _ = Describe("a removal whose drain is blocked", func() {
	It("names the blocking pod, warns, asks to be retried, and goes on once the pod is gone", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(1, secret.Name)
		cp1 := newTestControlPlane(cluster.Name, "d1")
		cp2 := newTestControlPlane(cluster.Name, "d2")
		cp3 := newTestControlPlane(cluster.Name, "d3")

		node := &corev1.Node{Name: drainRetryNode}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		kube := newDrainFake(func() int { return 0 }, node.DeepCopy(), drainTestPod("bare-pod", drainRetryNode, "", false))
		fake := &fakeTalosClient{nodeName: drainRetryNode}
		recorder := record.NewFakeRecorder(10)
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(fake), Recorder: recorder, KubeClient: kube,
		}

		for _, cp := range []*ichorv1alpha1.TalosMachine{cp1, cp2, cp3} {
			reconcileOK(r, cp)
		}

		var live ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, machineKeyOf(cp1), &live)).To(Succeed())
		live.Status.NodeName = drainRetryNode
		Expect(k8sClient.Status().Update(ctx, &live)).To(Succeed())

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())

		res, err := reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred(), "a blocked drain is a wait, not an error: the error backoff grows to 1000s")
		Expect(res.RequeueAfter).To(Equal(drainRetryInterval), "retried on the fixed short interval")

		cond := expectRemoval(fetchMachine(cp1), reasonDrainFailed)
		Expect(cond.Message).To(ContainSubstring("bare-pod"))
		Eventually(recorder.Events).Should(Receive(And(ContainSubstring(reasonDrainFailed), ContainSubstring("bare-pod"))))

		// A long blocked stretch keeps the same interval and does not repeat the warning.
		for range 19 {
			res, err = reconcileMachine(r, cp1)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.RequeueAfter).To(Equal(drainRetryInterval))
		}
		Expect(recorder.Events).To(BeEmpty(), "the Warning is emitted when the blocking state starts, not on every retry")

		Expect(kube.CoreV1().Pods("default").Delete(context.Background(), "bare-pod", metav1.DeleteOptions{})).To(Succeed())

		// The next retry, one interval later, finishes the removal.
		res, err = reconcileMachine(r, cp1)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		var gone ichorv1alpha1.TalosMachine
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, machineKeyOf(cp1), &gone))).To(BeTrue())
	})
})

var _ = Describe("markNodeReset", func() {
	It("annotates the Node of a reset machine once, and ignores a missing Node", func() {
		node := &corev1.Node{Name: "mark-reset-node"}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		r := &TalosMachineReconciler{Client: k8sClient}
		r.markNodeReset(ctx, "mark-reset-node")
		r.markNodeReset(ctx, "no-such-node")
		r.markNodeReset(ctx, "")

		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: node.Name}, &got)).To(Succeed())
		Expect(got.Annotations).To(HaveKey(nodeResetByIchorAnnotation))
	})
})
