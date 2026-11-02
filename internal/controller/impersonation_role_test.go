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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Ichor-ui may impersonate exactly the Ichor users that exist, and the groups their roles name.
var _ = Describe("impersonation role", func() {
	It("lists the IchorUsers and their role groups by name, and nothing when there are none", func() {
		ns := "ichor-impersonation"
		Expect(k8sClient.Create(ctx, &corev1.Namespace{Name: ns})).To(Succeed())

		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRole{Name: ImpersonationRoleName}))).To(Succeed())

		r := &ImpersonationRoleReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: ns}
		reconcileOnce := func() []rbacv1.PolicyRule {
			_, err := r.Reconcile(ctx, ctrl.Request{Name: ImpersonationRoleName})
			Expect(err).NotTo(HaveOccurred())

			var role rbacv1.ClusterRole
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ImpersonationRoleName}, &role)).To(Succeed())

			return role.Rules
		}

		Expect(reconcileOnce()).To(BeEmpty(), "no users: the role grants nothing (a rule without resourceNames would grant every name)")

		for name, roles := range map[string][]string{"user-a": {"admin"}, "user-b": {"viewer", "auditor"}} {
			Expect(k8sClient.Create(ctx, &ichorv1alpha1.IchorUser{
				Name: name, Namespace: ns, Spec: ichorv1alpha1.IchorUserSpec{Roles: roles},
			})).To(Succeed())
		}

		rules := reconcileOnce()
		Expect(rules).To(HaveLen(2))

		for _, rule := range rules {
			Expect(rule.Verbs).To(Equal([]string{"impersonate"}))
			Expect(rule.ResourceNames).NotTo(BeEmpty(), "every impersonate rule names who")

			switch rule.Resources[0] {
			case "users":
				Expect(rule.ResourceNames).To(Equal([]string{"ichor:user-a", "ichor:user-b"}))
			case "groups":
				Expect(rule.ResourceNames).To(Equal([]string{"ichor:admin", "ichor:auditor", "ichor:viewer"}))
			default:
				Fail("unexpected resource " + rule.Resources[0])
			}
		}

		var bob ichorv1alpha1.IchorUser
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "user-b"}, &bob)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &bob)).To(Succeed())

		for _, rule := range reconcileOnce() {
			if rule.Resources[0] == "users" {
				Expect(rule.ResourceNames).To(Equal([]string{"ichor:user-a"}), "a deleted user can no longer be impersonated")
			}
		}
	})
})
