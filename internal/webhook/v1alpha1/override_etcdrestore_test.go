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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0089: an etcd restore rewinds the whole cluster, so it needs override on the TalosCluster
// whichever way the TalosOperation is written.
var _ = Describe("Override webhook on an etcd restore", func() {
	const (
		plainUser = "restore-user"
		glassUser = "restore-glass-user"
	)

	grant := func(user string, glassOnCluster bool) {
		glass := ""
		if glassOnCluster {
			glass = "talosclusters"
		}

		grantOperationRights(user, glass)
	}

	restoreOp := func() *ichorv1alpha1.TalosOperation {
		return &ichorv1alpha1.TalosOperation{
			GenerateName: "restore-", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Kind: ichorv1alpha1.TalosOperationKindEtcdRestore,
				EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
					BackupRef: corev1.LocalObjectReference{Name: "nightly"}, Snapshot: "etcd-20261001T020000Z.snapshot.age",
					Confirm: testClusterName,
				},
			},
		}
	}

	BeforeEach(func() {
		grant(plainUser, false)
		grant(glassUser, true)
		grant(testControllerUser, false)
	})

	It("refuses a hand-written restore from a user without override on the cluster", func() {
		err := asUser(plainUser).Create(ctx, restoreOp())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("override"))
	})

	It("admits the restore when the user holds override on that cluster", func() {
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
