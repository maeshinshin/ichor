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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("applyKubeProxyObjects", func() {
	It("creates the kube-proxy objects from the manifest set, once, and leaves the rest alone", func() {
		meta := metav1.ObjectMeta{Namespace: testKubeSystem, Name: kubeProxyName}
		labels := map[string]string{labelK8sApp: kubeProxyName}
		other := metav1.ObjectMeta{Namespace: testKubeSystem, Name: "coredns-restore-test"}

		dsObj := &appsv1.DaemonSet{ObjectMeta: meta}
		dsObj.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		dsObj.Spec.Template.Labels = labels
		dsObj.Spec.Template.Spec.Containers = []corev1.Container{{Name: kubeProxyName, Image: "registry.k8s.io/kube-proxy:" + testK8sTarget}}

		objs := make([]map[string]any, 0, 3)

		for _, o := range []struct {
			obj runtime.Object
			gvk schema.GroupVersionKind
		}{
			{dsObj, appsv1.SchemeGroupVersion.WithKind("DaemonSet")},
			{&corev1.ServiceAccount{ObjectMeta: meta}, corev1.SchemeGroupVersion.WithKind("ServiceAccount")},
			{
				&corev1.ConfigMap{ObjectMeta: other},
				corev1.SchemeGroupVersion.WithKind("ConfigMap"),
			},
		} {
			m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o.obj)
			Expect(err).NotTo(HaveOccurred())

			u := &unstructured.Unstructured{Object: m}
			u.SetGroupVersionKind(o.gvk)
			objs = append(objs, u.Object)
		}

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &appsv1.DaemonSet{ObjectMeta: meta})
			_ = k8sClient.Delete(ctx, &corev1.ServiceAccount{ObjectMeta: meta})
		})

		Expect(kubeProxyExists()).To(BeFalse())
		Expect(applyKubeProxyObjects(ctx, k8sClient, objs)).To(Succeed())
		Expect(kubeProxyExists()).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: kubeProxyName}, &corev1.ServiceAccount{})).To(Succeed())

		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: testKubeSystem, Name: "coredns-restore-test"}, &corev1.ConfigMap{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		Expect(applyKubeProxyObjects(ctx, k8sClient, objs[:2])).To(Succeed(), "idempotent")
	})
})

var _ = Describe("removeKubeProxyObjects", func() {
	It("deletes the DaemonSet, ServiceAccount, ClusterRoleBinding and every kube-proxy ConfigMap, and nothing else", func() {
		newKubeProxyDaemonSet(testK8sOld, 2, 2)

		sa := &corev1.ServiceAccount{Namespace: testKubeSystem, Name: kubeProxyName}
		Expect(k8sClient.Create(ctx, sa)).To(Succeed())

		crb := &rbacv1.ClusterRoleBinding{
			Name:     kubeProxyName,
			RoleRef:  rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: clusterRoleKind, Name: "system:node-proxier"},
			Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: kubeProxyName, Namespace: testKubeSystem}},
		}
		Expect(k8sClient.Create(ctx, crb)).To(Succeed())

		// Talos names the ConfigMap kube-proxy-<checksum>; nothing guarantees it carries labels.
		cm := &corev1.ConfigMap{Namespace: testKubeSystem, Name: kubeProxyName + "-5d7f9c"}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		other := &corev1.ConfigMap{Namespace: testKubeSystem, Name: "coredns-kp-test"}
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
