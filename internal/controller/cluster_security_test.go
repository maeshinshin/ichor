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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The cluster's SecureBoot and KernelLockdown conditions summarise its approved machines'.
var _ = Describe("BootSecurityReconciler", func() {
	var (
		r       *BootSecurityReconciler
		cluster *ichorv1alpha1.TalosCluster
	)

	setup := func(sec *ichorv1alpha1.TalosClusterSecurity) {
		cluster = newTestCluster("boot-security", "v1.14")
		cluster.Spec.Security = sec
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		r = &BootSecurityReconciler{Client: k8sClient}
	}

	machine := func(approved bool, conds ...metav1.Condition) *ichorv1alpha1.TalosMachine {
		m := newTestMachine(cluster.Name, approved)
		for i := range conds {
			conds[i].LastTransitionTime = metav1.Now()
			conds[i].Message = "m"
			conds[i].Reason = "R"
		}

		m.Status.Conditions = conds
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		return m
	}

	cond := func(typ string) *metav1.Condition {
		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &got)).To(Succeed())

		return apimeta.FindStatusCondition(got.Status.Conditions, typ)
	}

	run := func() {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		Expect(err).NotTo(HaveOccurred())
	}

	preferred := &ichorv1alpha1.TalosClusterSecurity{
		SecureBoot: &ichorv1alpha1.TalosClusterSecureBoot{Mode: ichorv1alpha1.SecureBootPreferred},
	}

	It("is False and names the machines that do not match", func() {
		setup(preferred)

		bad := machine(true, metav1.Condition{
			Type: ichorv1alpha1.ConditionSecureBoot, Status: metav1.ConditionFalse, Reason: "SecureBootOff", Message: "x",
		})
		machine(true, metav1.Condition{
			Type: ichorv1alpha1.ConditionSecureBoot, Status: metav1.ConditionTrue, Reason: "SecureBootOn", Message: "x",
		})
		machine(false) // not approved: not counted

		run()

		c := cond(ichorv1alpha1.ConditionSecureBoot)
		Expect(c).NotTo(BeNil())
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Message).To(ContainSubstring(bad.Name))
		Expect(c.Message).To(ContainSubstring("1 of 2"))
		Expect(cond(ichorv1alpha1.ConditionKernelLockdown)).To(BeNil())
	})

	It("is True once every approved machine matches", func() {
		setup(&ichorv1alpha1.TalosClusterSecurity{
			SecureBoot: preferred.SecureBoot, KernelLockdown: ichorv1alpha1.KernelLockdownIntegrity,
		})

		for range 2 {
			machine(true,
				metav1.Condition{Type: ichorv1alpha1.ConditionSecureBoot, Status: metav1.ConditionTrue, Reason: "SecureBootOn", Message: "x"},
				metav1.Condition{Type: ichorv1alpha1.ConditionKernelLockdown, Status: metav1.ConditionTrue, Reason: "LockdownMet", Message: "x"})
		}

		run()
		run() // idempotent

		Expect(cond(ichorv1alpha1.ConditionSecureBoot).Status).To(Equal(metav1.ConditionTrue))
		Expect(cond(ichorv1alpha1.ConditionKernelLockdown).Status).To(Equal(metav1.ConditionTrue))
	})

	It("is Unknown while a machine has not been read", func() {
		setup(preferred)
		machine(true)
		run()

		Expect(cond(ichorv1alpha1.ConditionSecureBoot).Status).To(Equal(metav1.ConditionUnknown))
	})

	It("removes the conditions when the options are removed, and sets none for a cluster that never used them", func() {
		setup(preferred)
		machine(true)
		run()
		Expect(cond(ichorv1alpha1.ConditionSecureBoot)).NotTo(BeNil())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.Security = nil
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		run()
		Expect(cond(ichorv1alpha1.ConditionSecureBoot)).To(BeNil())
	})
})
