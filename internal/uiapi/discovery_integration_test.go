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

package uiapi

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

// fakeMaintenanceClient is controller.MaintenanceClient's test double: the
// insecure maintenance-mode identity read Discover performs.
type fakeMaintenanceClient struct {
	identity controller.HardwareIdentity
}

func (f *fakeMaintenanceClient) Identity(context.Context) (controller.HardwareIdentity, error) {
	return f.identity, nil
}

func (f *fakeMaintenanceClient) ApplyConfiguration(context.Context, []byte) error {
	return nil
}

func (f *fakeMaintenanceClient) Inventory(context.Context) (controller.Inventory, error) {
	return controller.Inventory{}, nil
}

func (f *fakeMaintenanceClient) SecurityState(context.Context) (controller.SecurityState, error) {
	return controller.SecurityState{}, nil
}

func (f *fakeMaintenanceClient) TalosVersion(context.Context) (string, error) { return "", nil }

// This file exercises the interaction RejectMachine's own doc comment
// relies on but cannot test from inside this package: that a rejected
// TalosMachine is never recreated as fresh and pending by
// internal/controller.DiscoveryReconciler.Discover when the same hardware
// reports in again. Discover is exported specifically so a caller outside
// internal/controller can drive it; this test does exactly that, against a
// real envtest API server, rather than asserting the property by reading
// Discover's source.
var _ = Describe("Discovery does not recreate a rejected machine", func() {
	It("returns the existing rejected machine unchanged on a repeat report", func() {
		cluster := newTestCluster("does-not-exist")

		// Discover names a new TalosMachine after the UUID itself,
		// and looks an existing one up the same way -- so this fixture must
		// be named identically, not through newTestMachine's GenerateName,
		// for "Discover finds the existing object" to be a meaningful check.
		identity := ichorv1alpha1.TalosMachineIdentity{
			UUID: "cccccccc-cccc-cccc-cccc-cccccccccccc", Serial: "serial-rejected", MACAddresses: []string{testMAC},
		}
		m := &ichorv1alpha1.TalosMachine{
			Name: identity.UUID, Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      identity,
				Endpoint:      "10.0.0.10:50000",
				ApprovalState: ichorv1alpha1.MachineApprovalStateRejected,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())

		var before ichorv1alpha1.TalosMachineList
		Expect(k8sClient.List(ctx, &before)).To(Succeed())
		countBefore := len(before.Items)

		r := &controller.DiscoveryReconciler{
			Client: k8sClient,
			MaintenanceClient: func(context.Context, string) (controller.MaintenanceClient, error) {
				return &fakeMaintenanceClient{identity: controller.HardwareIdentity{
					UUID: identity.UUID, Serial: identity.Serial, MACAddresses: identity.MACAddresses,
				}}, nil
			},
		}

		got, err := r.Discover(ctx, cluster, "10.0.0.10:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).NotTo(BeNil())
		Expect(got.Name).To(Equal(identity.UUID))
		Expect(got.Spec.ApprovalState).To(Equal(ichorv1alpha1.MachineApprovalStateRejected),
			"Discover must not reset a rejected machine's approval state")

		var after ichorv1alpha1.TalosMachineList
		Expect(k8sClient.List(ctx, &after)).To(Succeed())
		Expect(after.Items).To(HaveLen(countBefore), "Discover must not create a second TalosMachine for the same identity")
	})
})
