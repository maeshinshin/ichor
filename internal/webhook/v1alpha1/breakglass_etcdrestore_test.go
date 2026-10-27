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

// An etcd restore rewinds the whole cluster, so it needs break-glass on the TalosCluster
// whichever way the TalosOperation is written.
var _ = Describe("Break-glass webhook on an etcd restore", func() {
	const (
		plainUser = "restore-user"
		glassUser = "restore-glass-user"
	)

	grant := func(user string, glassOnCluster bool) {
		rules := []rbacv1.PolicyRule{{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosoperations"}, Verbs: []string{"create", "get"},
		}}
		if glassOnCluster {
			rules = append(rules, rbacv1.PolicyRule{
				APIGroups: []string{ichorGroup}, Resources: []string{"talosclusters"}, Verbs: []string{"break-glass"},
			})
		}

		role := &rbacv1.ClusterRole{Name: "bg-restore-" + user, Rules: rules}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, role))).To(Succeed())
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &rbacv1.ClusterRoleBinding{
			Name:     role.Name,
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: role.Name},
			Subjects: []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: "User", Name: user}},
		}))).To(Succeed())
	}

	restoreOp := func() *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{
			GenerateName: "restore-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Kind: ichorv1alpha1.TalosOperationKindEtcdRestore,
				EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
					BackupRef: corev1.LocalObjectReference{Name: "nightly"}, Snapshot: "etcd-20261001T020000Z.snapshot.age",
					Confirm: testClusterName, Reason: "bad change",
				},
			},
		}
	}

	BeforeEach(func() {
		grant(plainUser, false)
		grant(glassUser, true)
		grant(testControllerUser, false)
	})

	It("refuses a hand-written restore from a user without break-glass on the cluster", func() {
		err := asUser(plainUser).Create(ctx, restoreOp())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("break-glass"))
	})

	It("admits the restore when the user holds break-glass on that cluster", func() {
		Expect(asUser(glassUser).Create(ctx, restoreOp())).To(Succeed())
	})

	It("admits Ichor's own controller, which recreates the operation after the rollback", func() {
		Expect(asUser(testControllerUser).Create(ctx, restoreOp())).To(Succeed())
	})

	It("rejects a restore whose typed name is not the cluster's (the CRD rule)", func() {
		op := restoreOp()
		op.Spec.EtcdRestore.Confirm = "some-other-cluster"

		err := asUser(glassUser).Create(ctx, op)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("confirm"))
	})

	It("rejects the kind without spec.etcdRestore", func() {
		op := restoreOp()
		op.Spec.EtcdRestore = nil

		Expect(asUser(glassUser).Create(ctx, op)).NotTo(Succeed())
	})
})
