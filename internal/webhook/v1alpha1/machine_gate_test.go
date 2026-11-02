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
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// Spec.endpoint must be an address the cluster declares (spec.discovery) unless the user
// holds update on talosmachines/endpoint, and approving needs approve on talosmachines/approval.
var _ = Describe("Endpoint and approval gates on TalosMachine", func() {
	const (
		writer   = "machine-gate-writer"
		approver = "machine-gate-approver"
		pather   = "machine-gate-endpoint"
	)

	var cluster *ichorv1alpha1.TalosCluster

	machine := func(endpoint string) *ichorv1alpha1.TalosMachine {
		m := newTestMachine()
		m.Spec.ClusterRef = corev1.LocalObjectReference{Name: cluster.Name}
		m.Spec.Endpoint = endpoint

		return m
	}

	BeforeEach(func() {
		write := rbacv1.PolicyRule{APIGroups: []string{ichorGroup}, Resources: []string{bgUnitMachines},
			Verbs: []string{verbCreate, verbGet, verbUpdate, verbPatch, verbDelete}}
		bindRole("machine-gate-writer", writer, write)
		bindRole("machine-gate-approver", approver, write, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosmachines/approval"}, Verbs: []string{ichorv1alpha1.MachineApproveVerb},
		})
		bindRole("machine-gate-endpoint", pather, write, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosmachines/endpoint"}, Verbs: []string{ichorv1alpha1.MachineEndpointVerb},
		})

		cluster = newTestCluster()
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.168.50.7"}, CIDRs: []string{"192.168.60.0/24"}}
		cluster.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{"10.248.0.0/16"}}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		DeferCleanup(func() {
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			cluster.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		})
	})

	create := func(user string, m *ichorv1alpha1.TalosMachine) error {
		err := asUser(user).Create(ctx, m)
		if err == nil {
			DeferCleanup(func() {
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(m), m)).To(Succeed())
				m.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
				Expect(k8sClient.Update(ctx, m)).To(Succeed())
				Expect(k8sClient.Delete(ctx, m)).To(Succeed())
			})
		}

		return err
	}

	It("admits an endpoint inside the cluster's discovery addresses or CIDRs", func() {
		Expect(create(writer, machine("192.168.50.7:50000"))).To(Succeed())
		Expect(create(writer, machine("192.168.60.200:50000"))).To(Succeed())
	})

	It("refuses an endpoint outside discovery from a user without update on talosmachines/endpoint", func() {
		err := create(writer, machine("203.0.113.9:50000"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosmachines/endpoint"))
	})

	It("admits an endpoint outside discovery from a holder of update on talosmachines/endpoint", func() {
		Expect(create(pather, machine("203.0.113.9:50000"))).To(Succeed())
	})

	It("refuses loopback, link-local, unspecified, multicast and the cluster's own pod range for everyone", func() {
		for _, ep := range []string{"127.0.0.1:50000", "169.254.169.254:80", "0.0.0.0:50000", "224.0.0.1:50000", "10.248.1.1:50000"} {
			err := create(pather, machine(ep))
			Expect(err).To(HaveOccurred(), ep)
			Expect(err.Error()).To(ContainSubstring("endpoint"), ep)
			Expect(k8sClient.Create(ctx, machine(ep))).NotTo(Succeed(), ep)
		}
	})

	It("refuses changing spec.endpoint to an undeclared address", func() {
		m := machine("192.168.50.7:50000")
		Expect(create(writer, m)).To(Succeed())

		m.Spec.Endpoint = "203.0.113.9:50000"
		err := asUser(writer).Update(ctx, m)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosmachines/endpoint"))
	})

	It("leaves an update that does not touch the endpoint alone, even when discovery no longer lists it", func() {
		m := machine("203.0.113.9:50000")
		Expect(create(pather, m)).To(Succeed())

		m.Spec.Hostname = "worker-9"
		Expect(asUser(writer).Update(ctx, m)).To(Succeed())
	})

	It("refuses creating an approved machine without approve on talosmachines/approval", func() {
		m := machine("192.168.50.7:50000")
		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved

		err := create(writer, m)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosmachines/approval"))
		Expect(create(approver, m)).To(Succeed())
	})

	It("refuses moving a pending machine to approved without the verb", func() {
		m := machine("192.168.50.7:50000")
		Expect(create(writer, m)).To(Succeed())

		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
		err := asUser(writer).Update(ctx, m)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("talosmachines/approval"))
		Expect(asUser(approver).Update(ctx, m)).To(Succeed())
	})

	It("does not ask for approve when the machine stays approved or is rejected", func() {
		m := machine("192.168.50.7:50000")
		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
		Expect(create(approver, m)).To(Succeed())

		m.Spec.Hostname = "worker-8"
		Expect(asUser(writer).Update(ctx, m)).To(Succeed())

		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected
		Expect(asUser(writer).Update(ctx, m)).To(Succeed())
	})
})
