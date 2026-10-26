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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("removeKubeProxyObjects", func() {
	It("deletes the DaemonSet, ServiceAccount, ClusterRoleBinding and every kube-proxy ConfigMap, and nothing else", func() {
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: testKubeSystem, Name: kubeProxyName}}
		Expect(k8sClient.Create(ctx, sa)).To(Succeed())

		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: kubeProxyName},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "system:node-proxier"},
			Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: kubeProxyName, Namespace: testKubeSystem}},
		}
		Expect(k8sClient.Create(ctx, crb)).To(Succeed())

		// Talos names the ConfigMap kube-proxy-<checksum>; nothing guarantees it carries labels.
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: testKubeSystem, Name: kubeProxyName + "-5d7f9c"}}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		other := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: testKubeSystem, Name: "coredns-kp-test"}}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, other) })

		Expect(removeKubeProxyObjects(ctx, k8sClient)).To(Succeed())

		for _, o := range []client.Object{sa, crb, cm} {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(o), o)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "%T %s should be gone", o, o.GetName())
		}

		Expect(kubeProxyExists()).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(other), other)).To(Succeed())

		Expect(removeKubeProxyObjects(ctx, k8sClient)).To(Succeed(), "idempotent")
	})
})
