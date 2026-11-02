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

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The etcd-restore and ca-rotation finalizers on a TalosOperation hold a half
// finished restore or rotation; stripping one is break-glass, like stripping the machine finalizer.
var _ = Describe("Break-glass webhook on TalosOperation finalizers", func() {
	const (
		editor = "opfin-editor"
		glass  = "opfin-glass"
	)

	var op *ichorv1alpha1.TalosOperation

	BeforeEach(func() {
		edit := rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{resOperations}, Verbs: []string{verbGet, verbUpdate, verbPatch},
		}
		bindRole("opfin-editor", editor, edit)
		bindRole("opfin-glass", glass, edit, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{resOperations}, Verbs: []string{BreakGlassVerb},
		})
		bindRole("opfin-controller", testControllerUser, edit)

		op = &ichorv1alpha1.TalosOperation{
			GenerateName: "fin-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				Kind: ichorv1alpha1.TalosOperationKindTalos, ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			},
		}
		op.Finalizers = []string{"ichor.maesh.dev/ca-rotation"}
		Expect(k8sClient.Create(ctx, op)).To(Succeed())
		Expect(k8sClient.Delete(ctx, op)).To(Succeed())

		DeferCleanup(func() {
			var o ichorv1alpha1.TalosOperation
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(op), &o); err != nil {
				return
			}

			o.Finalizers = nil
			_ = k8sClient.Update(ctx, &o)
		})
	})

	strip := func(user string) error {
		var o ichorv1alpha1.TalosOperation
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(op), &o)).To(Succeed())
		o.Finalizers = nil

		return asUser(user).Update(ctx, &o)
	}

	It("refuses a user without break-glass who strips the finalizer", func() {
		err := strip(editor)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("finalizer"))
	})

	It("admits a holder of break-glass and Ichor's own controller", func() {
		Expect(strip(glass)).To(Succeed())
	})

	It("admits the controller", func() {
		Expect(strip(testControllerUser)).To(Succeed())
	})
})
