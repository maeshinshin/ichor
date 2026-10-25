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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// loadBundle only ever reads cluster.Spec.SecretsRef.Name -- never
// cluster.Namespace, -- so these tests build a TalosCluster
// value directly rather than creating one through the API server: a
// namespace that merely differs from tmNamespace is all "lives elsewhere"
// needs, and loadBundle's own namespace argument is what actually gets
// exercised against the real Secret below.
var _ = Describe("loadBundle", func() {
	It("resolves the Secret in the given namespace even when the TalosCluster's namespace differs", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle) // lives in tmNamespace, the stand-in for the controller's own namespace.

		cluster := &ichorv1alpha1.TalosCluster{
			Spec: ichorv1alpha1.TalosClusterSpec{SecretsRef: corev1.LocalObjectReference{Name: secret.Name}},
		}
		cluster.Namespace = "ichor-e2e" // never created; deliberately not tmNamespace.

		got, err := loadBundle(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Cluster.ID).To(Equal(bundle.Cluster.ID))
	})

	It("fails explicitly, without touching the API, when the controller namespace is empty", func() {
		cluster := &ichorv1alpha1.TalosCluster{
			Spec: ichorv1alpha1.TalosClusterSpec{SecretsRef: corev1.LocalObjectReference{Name: "irrelevant"}},
		}

		_, err := loadBundle(ctx, k8sClient, "", cluster)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("controller namespace is empty"))
	})

	It("names the namespace it looked in when the Secret is missing", func() {
		cluster := &ichorv1alpha1.TalosCluster{
			Spec: ichorv1alpha1.TalosClusterSpec{SecretsRef: corev1.LocalObjectReference{Name: "no-such-secret"}},
		}

		_, err := loadBundle(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(tmNamespace + "/no-such-secret"))
	})
})
