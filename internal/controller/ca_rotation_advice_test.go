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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("CARotationAdvised condition", func() {
	advice := func(cluster *ichorv1alpha1.TalosCluster) *metav1.Condition {
		key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
		_, err := reconcileCluster(&TalosClusterReconciler{Client: k8sClient}, cluster)
		Expect(err).NotTo(HaveOccurred())

		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

		return apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionCARotationAdvised)
	}

	adopted := func() *ichorv1alpha1.TalosCluster {
		c := newTestCluster("advice-secret", "v1.14")
		c.Annotations = map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())

		return c
	}

	It("does nothing on a cluster that was not adopted", func() {
		Expect(advice(newTestCluster("advice-secret", "v1.14"))).To(BeNil())
	})

	It("sets the advice True, reason Adopted, on an adopted cluster", func() {
		cond := advice(adopted())
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Adopted"))
	})

	It("lowers the advice once it is dismissed", func() {
		c := adopted()
		Expect(advice(c).Status).To(Equal(metav1.ConditionTrue))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: c.Name}, c)).To(Succeed())
		c.Annotations[ichorv1alpha1.TalosClusterCARotationAdviceDismissedAnnotation] = resetOnDeleteValue
		Expect(k8sClient.Update(ctx, c)).To(Succeed())

		cond := advice(c)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Dismissed"))
	})

	It("lowers the advice when a CA rotation has completed", func() {
		c := adopted()
		Expect(advice(c).Status).To(Equal(metav1.ConditionTrue))

		op := &ichorv1alpha1.TalosOperation{}
		op.GenerateName, op.Namespace = "advice-op-", tmNamespace
		op.Spec.ClusterRef.Name, op.Spec.Kind = c.Name, ichorv1alpha1.TalosOperationKindCARotation
		Expect(k8sClient.Create(ctx, op)).To(Succeed())

		Expect(advice(c).Status).To(Equal(metav1.ConditionTrue), "an unfinished rotation keeps the advice")

		op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		cond := advice(c)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Rotated"))
	})
})
