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
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// Versions the webhook tests repeat.
const (
	testTalosVersion    = "v1.14.3"
	testVersionContract = "v1.14"
	testK8sVersion      = "v1.36.2"
)

func newTestCluster() *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		GenerateName: "cluster-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.1"},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: "v1.36.0",
			VersionContract:   testVersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "secrets"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: "true"},
		},
	}
}

var _ = Describe("TalosCluster Webhook", func() {
	var obj *ichorv1alpha1.TalosCluster

	BeforeEach(func() {
		obj = newTestCluster()
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	})

	AfterEach(func() { forceDelete(obj) })

	It("admits create", func() {
		// obj was already created in BeforeEach; nothing further to assert.
	})

	It("fills spec.driftPolicy at create through the mutating webhook", func() {
		Expect(obj.Spec.DriftPolicy).To(Equal("warnOnly"))
	})

	It("refuses an update that makes a vip endpoint a hostname", func() {
		obj.Spec.Endpoint.Address = "cluster.example.com"
		err := k8sClient.Update(ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("needs an IP address"))
	})

	It("refuses to create a vip cluster whose endpoint is a hostname", func() {
		bad := newTestCluster()
		bad.Spec.Endpoint.Address = "cluster.example.com"
		err := k8sClient.Create(ctx, bad)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("type loadBalancer would take a hostname"))
	})

	It("admits update", func() {
		obj.Spec.KubernetesVersion = "v1.36.1"
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
	})

	It("refuses deletion without the allow-delete annotation", func() {
		err := k8sClient.Delete(ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(deleteguard.AllowDeleteAnnotation))
	})

	It("allows deletion with ichor.maesh.dev/allow-delete: \"true\"", func() {
		obj.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
		Expect(k8sClient.Update(ctx, obj)).To(Succeed())
		Expect(k8sClient.Delete(ctx, obj)).To(Succeed())
	})
})
