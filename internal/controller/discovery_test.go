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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// fakeMaintenanceClient is the maintenance-mode Talos API boundary test
// double, the discovery equivalent of fakeTalosClient: no test here ever
// dials a real node (unit rule 2).
type fakeMaintenanceClient struct {
	identity HardwareIdentity
	err      error
}

func (f *fakeMaintenanceClient) Identity(context.Context) (HardwareIdentity, error) {
	return f.identity, f.err
}

func maintenanceFactory(fake *fakeMaintenanceClient) func(context.Context, string) (MaintenanceClient, error) {
	return func(context.Context, string) (MaintenanceClient, error) {
		return fake, nil
	}
}

// getMachine fetches the TalosMachine named name in namespace, or nil if it
// does not exist -- discovery_test.go's own tests care about exactly this
// question (did Discover create or touch the object named after this
// UUID?), scoped by name rather than by listing the namespace, since
// envtest's apiserver is shared across every spec in this suite and other
// specs leave their own machines behind in the same namespace.
func getMachine(namespace, name string) *ichorv1alpha1.TalosMachine {
	var m ichorv1alpha1.TalosMachine

	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &m)
	if apierrors.IsNotFound(err) {
		return nil
	}

	Expect(err).NotTo(HaveOccurred())

	return &m
}

// Each test below uses its own UUID so that none of discovery_test.go's
// specs can collide with another's objects in the shared "default"
// namespace every TalosCluster in this suite is created in.
const (
	uuidBoundary  = "00000000-0000-0000-0000-000000000000"
	uuidWeak      = "10101010-1010-1010-1010-101010101010"
	uuidCreate    = "20202020-2020-2020-2020-202020202020"
	uuidLink      = "30303030-3030-3030-3030-303030303030"
	uuidCollision = "40404040-4040-4040-4040-404040404040"
	uuidApproval  = "50505050-5050-5050-5050-505050505050"

	// testMAC is the permanent MAC address every test below that doesn't
	// care about its specific value reuses.
	testMAC = "aa:bb:cc:dd:ee:ff"
)

var _ = Describe("strongIdentity", func() {
	DescribeTable("boundary cases over the UUID/serial/MAC tuple",
		func(hw HardwareIdentity, want bool) {
			Expect(strongIdentity(hw)).To(Equal(want))
		},
		Entry("all three present", HardwareIdentity{UUID: uuidBoundary, Serial: "S1", MACAddresses: []string{testMAC}}, true),
		Entry("missing UUID", HardwareIdentity{Serial: "S1", MACAddresses: []string{testMAC}}, false),
		Entry("missing serial", HardwareIdentity{UUID: uuidBoundary, MACAddresses: []string{testMAC}}, false),
		Entry("missing MAC addresses", HardwareIdentity{UUID: uuidBoundary, Serial: "S1"}, false),
		Entry("missing UUID and serial", HardwareIdentity{MACAddresses: []string{testMAC}}, false),
		Entry("missing serial and MAC addresses", HardwareIdentity{UUID: uuidBoundary}, false),
		Entry("missing UUID and MAC addresses", HardwareIdentity{Serial: "S1"}, false),
		Entry("everything empty", HardwareIdentity{}, false),
	)
})

var _ = Describe("DiscoveryReconciler.Discover", func() {
	It("does not auto-register a weak identity, and reports it instead of creating anything", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		recorder := record.NewFakeRecorder(10)
		// Strong on UUID and MAC, but no serial -- still a weak key.
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{UUID: uuidWeak, MACAddresses: []string{testMAC}}}

		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.20:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())

		Expect(getMachine(cluster.Namespace, uuidWeak)).To(BeNil())
		Eventually(recorder.Events).Should(Receive(ContainSubstring(reasonWeakIdentity)))
	})

	It("creates a pending, unapproved TalosMachine for a strong identity", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidCreate, Serial: "S1", MACAddresses: []string{testMAC}, Manufacturer: "QEMU",
		}}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.21:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).NotTo(BeNil())

		Expect(m.Name).To(Equal(uuidCreate))
		Expect(m.Spec.Approved).To(BeFalse())
		Expect(m.Spec.Role).To(BeEmpty())
		Expect(m.Spec.Endpoint).To(Equal("10.0.0.21:50000"))
		Expect(m.Spec.Identity).To(Equal(ichorv1alpha1.TalosMachineIdentity{
			UUID: uuidCreate, Serial: "S1", MACAddresses: []string{testMAC},
		}))

		got := getMachine(cluster.Namespace, uuidCreate)
		Expect(got).NotTo(BeNil())
		Expect(got.Status.Conditions).To(BeEmpty())
	})

	It("links to the existing TalosMachine, and creates nothing new, when the same machine is discovered again", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidLink, Serial: "S2", MACAddresses: []string{"11:22:33:44:55:66"},
		}}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		first, err := r.Discover(ctx, cluster, "10.0.0.22:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(first).NotTo(BeNil())

		second, err := r.Discover(ctx, cluster, "10.0.0.22:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(second).NotTo(BeNil())

		Expect(second.Name).To(Equal(first.Name))
		Expect(second.ResourceVersion).To(Equal(first.ResourceVersion))
	})

	It("does not auto-register a colliding UUID and leaves the existing TalosMachine untouched", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")

		existing := &ichorv1alpha1.TalosMachine{
			Name: uuidCollision, Namespace: cluster.Namespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
				Identity: ichorv1alpha1.TalosMachineIdentity{
					UUID: uuidCollision, Serial: "original", MACAddresses: []string{"aa:aa:aa:aa:aa:aa"},
				},
				Endpoint: "10.0.0.30:50000",
			},
		}
		Expect(k8sClient.Create(ctx, existing)).To(Succeed())

		recorder := record.NewFakeRecorder(10)
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidCollision, Serial: "different", MACAddresses: []string{"bb:bb:bb:bb:bb:bb"},
		}}

		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.31:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())

		Eventually(recorder.Events).Should(Receive(ContainSubstring(reasonIdentityCollision)))

		got := getMachine(cluster.Namespace, uuidCollision)
		Expect(got).NotTo(BeNil())
		Expect(got.Spec.Identity.Serial).To(Equal("original"))
		Expect(got.Spec.Endpoint).To(Equal("10.0.0.30:50000"))
	})

	It("never applies configuration to a discovered machine before it is approved", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidApproval, Serial: "S1", MACAddresses: []string{testMAC},
		}}

		dr := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}
		m, err := dr.Discover(ctx, cluster, "10.0.0.40:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).NotTo(BeNil())

		// A fake TalosClient that would record any call made against it --
		// if the configuration reconciler ever reached it for an unapproved
		// machine, this test would see it.
		talosFake := &fakeTalosClient{}
		cr := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), ControllerNamespace: tmNamespace, TalosClient: clientFactory(talosFake)}

		_, err = reconcileMachine(cr, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(talosFake.dryRunCalls).To(BeZero())
		Expect(talosFake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.Conditions).To(BeEmpty())
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())
	})
})
