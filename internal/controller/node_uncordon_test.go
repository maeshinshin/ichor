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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("uncordonRejoinedNode", func() {
	newNode := func(name string, ready, cordoned bool, annotations map[string]string) *corev1.Node {
		node := &corev1.Node{Name: name}
		node.Annotations = annotations
		node.Spec.Unschedulable = cordoned
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}

		node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
		Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

		return node
	}

	get := func(name string) corev1.Node {
		var got corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, &got)).To(Succeed())

		return got
	}

	It("uncordons a Ready Node Ichor reset or cordoned, and drops the markers", func() {
		r := &TalosMachineReconciler{Client: k8sClient}

		newNode("rejoin-reset", true, true, map[string]string{nodeResetByIchorAnnotation: "2026-11-02T00:00:00Z"})
		newNode("rejoin-marker", true, true, map[string]string{cordonedByIchorAnnotation: "removal"})

		for _, name := range []string{"rejoin-reset", "rejoin-marker"} {
			Expect(r.uncordonRejoinedNode(ctx, name)).To(Succeed())

			got := get(name)
			Expect(got.Spec.Unschedulable).To(BeFalse(), name)
			Expect(got.Annotations).NotTo(HaveKey(nodeResetByIchorAnnotation))
			Expect(got.Annotations).NotTo(HaveKey(cordonedByIchorAnnotation))
		}
	})

	It("leaves a Node a human cordoned, one that is not Ready yet, and a missing one", func() {
		r := &TalosMachineReconciler{Client: k8sClient}

		newNode("rejoin-human", true, true, nil)
		newNode("rejoin-notready", false, true, map[string]string{nodeResetByIchorAnnotation: "2026-11-02T00:00:00Z"})

		Expect(r.uncordonRejoinedNode(ctx, "rejoin-human")).To(Succeed())
		Expect(r.uncordonRejoinedNode(ctx, "rejoin-notready")).To(Succeed())
		Expect(r.uncordonRejoinedNode(ctx, "no-such-node")).To(Succeed())
		Expect(r.uncordonRejoinedNode(ctx, "")).To(Succeed())

		for _, name := range []string{"rejoin-human", "rejoin-notready"} {
			got := get(name)
			Expect(got.Spec.Unschedulable).To(BeTrue(), name)
		}

		Expect(get("rejoin-notready").Annotations).To(HaveKey(nodeResetByIchorAnnotation))
	})

	It("is the removal drain that marks its own cordon, and only that", func() {
		kube := newDrainFake(func() int { return 0 }, &corev1.Node{Name: "mark-fresh"}, &corev1.Node{Name: "mark-human"})
		r := &TalosMachineReconciler{Client: k8sClient, KubeClient: kube}

		fresh := newNode("mark-fresh", true, false, nil)
		human := newNode("mark-human", true, true, nil)

		Expect(r.drainNode(ctx, fresh.Name)).To(Succeed())
		Expect(r.drainNode(ctx, human.Name)).To(Succeed())

		Expect(get(fresh.Name).Annotations).To(HaveKey(cordonedByIchorAnnotation))
		Expect(get(human.Name).Annotations).NotTo(HaveKey(cordonedByIchorAnnotation))
	})
})
