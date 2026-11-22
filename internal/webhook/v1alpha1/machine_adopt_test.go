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
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/cli"
)

// ichor init adopts the pending TalosMachine discovery created for its first node (cli.AdoptDiscoveredMachine):
// an update from pending to an approved control plane, which has to pass the CRD rules and the machine webhook.
var _ = Describe("ichor init adopting a discovered TalosMachine", func() {
	It("is admitted by the API server and leaves an approved control plane", func() {
		cluster := newTestCluster()
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.168.50.9"}}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		identity := ichorv1alpha1.TalosMachineIdentity{
			UUID: "00000000-0000-0000-0000-0000000000ad", Serial: "S-adopt", MACAddresses: []string{testMAC1},
		}
		// What discovery creates: pending, no role.
		pending := &ichorv1alpha1.TalosMachine{
			Name: identity.UUID, Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: identity, Endpoint: "192.168.50.9:50000",
			},
		}
		Expect(k8sClient.Create(ctx, pending)).To(Succeed())
		// Not deleted: the webhook refuses deleting the last control plane, even with allow-delete.
		DeferCleanup(func() { forceDelete(cluster) })

		// What init wants: the same node as an approved control plane with its install disk.
		mine := pending.DeepCopy()
		mine.ResourceVersion, mine.UID = "", ""
		mine.Spec.Role = roleControlPlane
		mine.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
		mine.Spec.Install = &ichorv1alpha1.TalosMachineInstall{Disk: ichorv1alpha1.TalosMachineDisk{Serial: "D1"}}

		createErr := k8sClient.Create(ctx, mine)
		Expect(createErr).To(HaveOccurred(), "positive control: the create alone is refused as AlreadyExists")

		Expect(cli.AdoptDiscoveredMachine(ctx, k8sClient, mine, createErr)).To(Succeed())

		var got ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: identity.UUID}, &got)).To(Succeed())
		Expect(got.Spec.Role).To(Equal(roleControlPlane))
		Expect(got.Spec.IsApproved()).To(BeTrue())
		Expect(got.UID).To(Equal(pending.UID), "the pending object was updated, not replaced")
	})
})
