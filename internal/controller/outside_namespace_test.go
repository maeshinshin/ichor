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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// An object an older release left outside the controller's namespace keeps reconciling and gets a warning (ADR 0175).
var _ = Describe("OutsideNamespaceReconciler", func() {
	reconcileOutside := func(ns string, cluster *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCluster {
		r := &OutsideNamespaceReconciler{Client: k8sClient, Namespace: ns, New: func() client.Object { return &ichorv1alpha1.TalosCluster{} }}
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(cluster)})
		Expect(err).NotTo(HaveOccurred())

		return getCluster(cluster)
	}

	It("flags a cluster outside the namespace and leaves its reconciling alone", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		got := reconcileOutside("ichor-system-elsewhere", cluster)

		c := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionOutsideControllerNamespace)
		Expect(c).NotTo(BeNil())
		Expect(string(c.Status)).To(Equal("True"))
		Expect(c.Message).To(ContainSubstring("ichor-system-elsewhere"))

		// The ordinary reconcile still runs on it.
		factory := func(context.Context, string) (MaintenanceClient, error) {
			return &fakeMaintenanceClient{identity: HardwareIdentity{UUID: uuidOutsideNS, Serial: "S1", MACAddresses: []string{testMAC}}}, nil
		}
		_, err := reconcileCluster(newDiscoveryReconciler(factory), cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(getMachine(cluster.Namespace, uuidOutsideNS)).NotTo(BeNil())
	})

	It("leaves a cluster inside the namespace without the condition", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")

		got := reconcileOutside(cluster.Namespace, cluster)
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionOutsideControllerNamespace)).To(BeNil())
	})
})

const uuidOutsideNS = "91919191-9191-9191-9191-919191919191"
