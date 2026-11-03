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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// topologyCluster creates a TalosCluster with the given control-plane count
// and schedulable value, returning its generated name.
func topologyCluster(count int32, schedulable string) string {
	c := newTestCluster()
	c.Spec.ControlPlane = ichorv1alpha1.TalosClusterControlPlane{Count: count, Schedulable: schedulable}
	Expect(k8sClient.Create(ctx, c)).To(Succeed())

	return c.Name
}

// topologyMachine builds a TalosMachine with a fresh identity so tests never
// share a UUID or endpoint.
func topologyMachine(cluster, role, approval string) *ichorv1alpha1.TalosMachine {
	id := string(uuid.NewUUID())

	return &ichorv1alpha1.TalosMachine{
		GenerateName: "topo-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: cluster},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: id},
			Endpoint:      fmt.Sprintf("10.9.%d.%d:50000", id[0], id[1]),
			Role:          role,
			ApprovalState: approval,
		},
	}
}

// grantLastWorkerRights lets user read and write clusters and machines, and (if glass) hold the
// override verb on both.
func grantLastWorkerRights(user string, glass bool) {
	bindRole("lw-test-"+user, user, rbacv1.PolicyRule{
		APIGroups: []string{ichorGroup}, Resources: []string{resClusters, bgUnitMachines}, Verbs: writeVerbs(glass),
	})
}

// approve creates m pending, then approves it by update, the way the UI and
// CLI do; it returns the update's error.
func approve(m *ichorv1alpha1.TalosMachine) error {
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending
	Expect(k8sClient.Create(ctx, m)).To(Succeed())
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved

	return k8sClient.Update(ctx, m)
}

func clusterKey(name string) types.NamespacedName {
	return types.NamespacedName{Namespace: testNamespace, Name: name}
}

var _ = Describe("TalosMachine Webhook control-plane approval cap", func() {
	It("allows a replacement control plane (+1) but refuses a second extra one", func() {
		cluster := topologyCluster(1, "true")

		Expect(approve(topologyMachine(cluster, roleControlPlane, ""))).To(Succeed()) // 0 -> 1
		Expect(approve(topologyMachine(cluster, roleControlPlane, ""))).To(Succeed()) // 1 -> 2: replacement

		err := approve(topologyMachine(cluster, roleControlPlane, "")) // 2 -> 3 on a count of 1
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.controlPlane.count"))
	})

	It("refuses creating an already-approved control plane beyond the cap", func() {
		cluster := topologyCluster(1, "true")
		approved := ichorv1alpha1.MachineApprovalStateApproved
		Expect(k8sClient.Create(ctx, topologyMachine(cluster, roleControlPlane, approved))).To(Succeed())
		Expect(k8sClient.Create(ctx, topologyMachine(cluster, roleControlPlane, approved))).To(Succeed())

		err := k8sClient.Create(ctx, topologyMachine(cluster, roleControlPlane, approved))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.controlPlane.count"))
	})

	It("does not cap workers", func() {
		cluster := topologyCluster(1, "true")
		for range 4 {
			Expect(approve(topologyMachine(cluster, roleWorker, ""))).To(Succeed())
		}
	})
})

var _ = Describe("schedulable: false with zero workers", func() {
	setSchedulableAs := func(cl client.Client, cluster, value string) error {
		c := &ichorv1alpha1.TalosCluster{}
		Expect(k8sClient.Get(ctx, clusterKey(cluster), c)).To(Succeed())
		c.Spec.ControlPlane.Schedulable = value

		return cl.Update(ctx, c)
	}
	setSchedulable := func(cluster, value string) error { return setSchedulableAs(k8sClient, cluster, value) }

	// The suite's own client is cluster-admin and so holds override; the guard's refusals are
	// checked as a user who does not (ADR 0065, amended), and the override as one who does.
	BeforeEach(func() {
		grantLastWorkerRights(plainUser, false)
		grantLastWorkerRights(glassUser, true)
	})

	It("refuses setting schedulable to false while the cluster has no approved worker", func() {
		cluster := topologyCluster(3, "true")
		// A pending worker has not joined, so it does not count.
		Expect(k8sClient.Create(ctx, topologyMachine(cluster, roleWorker, ichorv1alpha1.MachineApprovalStatePending))).To(Succeed())

		err := setSchedulableAs(asUser(plainUser), cluster, "false")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("schedulable"))
		Expect(err.Error()).To(ContainSubstring("override"))

		Expect(setSchedulableAs(asUser(glassUser), cluster, "false")).To(Succeed())
	})

	It("lets an override holder remove the last worker and refuses a plain user", func() {
		cluster := topologyCluster(3, "false")
		w := topologyMachine(cluster, roleWorker, "")
		Expect(approve(w)).To(Succeed())
		allowDelete(w)
		Expect(k8sClient.Update(ctx, w)).To(Succeed())

		err := asUser(plainUser).Delete(ctx, w)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("override"))

		Expect(asUser(glassUser).Delete(ctx, w)).To(Succeed())
	})

	It("allows schedulable false once an approved worker exists, then refuses removing the last worker", func() {
		cluster := topologyCluster(3, "true")
		w1 := topologyMachine(cluster, roleWorker, "")
		Expect(approve(w1)).To(Succeed())
		Expect(setSchedulable(cluster, "false")).To(Succeed())

		allowDelete(w1)
		Expect(k8sClient.Update(ctx, w1)).To(Succeed())
		err := asUser(plainUser).Delete(ctx, w1)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("schedulable"))

		// With a second worker, removing one is fine.
		Expect(approve(topologyMachine(cluster, roleWorker, ""))).To(Succeed())
		Expect(k8sClient.Delete(ctx, w1)).To(Succeed())
	})

	It("refuses un-approving the last worker of an unschedulable cluster", func() {
		cluster := topologyCluster(3, "false")
		w := topologyMachine(cluster, roleWorker, "")
		Expect(approve(w)).To(Succeed())

		w.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateRejected
		err := asUser(plainUser).Update(ctx, w)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("schedulable"))
	})

	It("lets a schedulable cluster lose all its workers", func() {
		cluster := topologyCluster(3, "true")
		w := topologyMachine(cluster, roleWorker, "")
		Expect(approve(w)).To(Succeed())
		allowDelete(w)
		Expect(k8sClient.Update(ctx, w)).To(Succeed())
		Expect(k8sClient.Delete(ctx, w)).To(Succeed())
	})
})
