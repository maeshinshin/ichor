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
	"crypto/tls"
	"crypto/x509"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cryptox509 "github.com/siderolabs/crypto/x509"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A node that is reset comes back in maintenance mode with a new certificate and a possibly different
// disk set. Its pending TalosMachine pinned the old certificate; discovery has to refresh it, or the
// machine sticks at MaintenanceIdentityChanged once approved.
var _ = Describe("DiscoveryReconciler.DiscoverAll pending machines", func() {
	const (
		oldFP = "OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o="
		newFP = "mP0qkFlQXzZ0qFqjTz3o9m6zI5Qq0o3d3E3b9w5oYQ0="
	)

	var (
		oldDisks = []ichorv1alpha1.TalosMachineDiscoveredDisk{{Serial: "D-old", DevPath: "/dev/vda", SizeBytes: 1 << 30}}
		newDisks = []ichorv1alpha1.TalosMachineDiscoveredDisk{{Serial: "D-new", DevPath: "/dev/vdb", SizeBytes: 2 << 30}}
	)

	// machineAt creates a machine of the cluster at host with the given approval state and a recorded
	// pin and inventory, as a discovery that has run once leaves it.
	machineAt := func(cluster *ichorv1alpha1.TalosCluster, name, host, serial, approval string) *ichorv1alpha1.TalosMachine {
		m := &ichorv1alpha1.TalosMachine{
			Name: name, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: name, Serial: serial, MACAddresses: []string{testMAC}},
				Endpoint:      host + ":50000",
				ApprovalState: approval,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, m) })

		m.Status.MaintenanceFingerprint = oldFP
		m.Status.DiscoveredDisks = oldDisks
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		return m
	}

	clusterAt := func(name string, hosts ...string) *ichorv1alpha1.TalosCluster {
		return &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: name, Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: hosts},
		}}
	}

	// node is a reset machine: this identity, a new certificate and a new disk.
	node := func(uuid, serial string) *fakeMaintenanceClient {
		return &fakeMaintenanceClient{
			identity:    HardwareIdentity{UUID: uuid, Serial: serial, MACAddresses: []string{testMAC}},
			fingerprint: newFP,
			inventory:   Inventory{Disks: newDisks},
		}
	}

	It("refreshes the pin and the inventory of a pending machine that answers with the same identity and a new certificate", func() {
		cluster := clusterAt("refresh-cluster", "198.51.100.30")
		machineAt(cluster, "refresh-pending", "198.51.100.30", "S-refresh", ichorv1alpha1.MachineApprovalStatePending)

		recorder := record.NewFakeRecorder(10)
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder,
			MaintenanceClient: maintenanceFactory(node("refresh-pending", "S-refresh"))}

		found, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty(), "a machine that was already known is not a new discovery")

		got := getMachine(tmNamespace, "refresh-pending")
		Expect(got.Status.MaintenanceFingerprint).To(Equal(newFP))
		Expect(got.Status.DiscoveredDisks).To(Equal(newDisks))
		Expect(got.Annotations).NotTo(HaveKey(ichorv1alpha1.TalosMachineTrustMaintenanceFingerprintAnnotation),
			"the refresh is the controller's record, not an operator's trust decision")
		Expect(got.Spec.Endpoint).To(Equal("198.51.100.30:50000"), "the controller never mutates spec")
		Expect(got.Spec.IsApproved()).To(BeFalse())
		Expect(recorder.Events).To(Receive(And(ContainSubstring("MaintenanceIdentityRefreshed"), ContainSubstring(newFP))))
	})

	It("lets the refreshed pin accept the new certificate and still refuse another, so approval then works", func() {
		cluster := clusterAt("refresh-pin-cluster", "198.51.100.31")
		machineAt(cluster, "refresh-pin", "198.51.100.31", "S-pin", ichorv1alpha1.MachineApprovalStatePending)

		oldCert, newCert := selfSigned(GinkgoT()), selfSigned(GinkgoT())
		parse := func(c tls.Certificate) *x509.Certificate {
			parsed, err := x509.ParseCertificate(c.Certificate[0])
			Expect(err).NotTo(HaveOccurred())

			return parsed
		}

		fake := node("refresh-pin", "S-pin")
		fake.fingerprint = cryptox509.SPKIFingerprint(parse(newCert)).String()
		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// The pin the provisioning builds from status (no trust annotation) accepts the node's new
		// certificate; the other one is refused.
		pin, err := NewMaintenancePin(getMachine(tmNamespace, "refresh-pin").Status.MaintenanceFingerprint)
		Expect(err).NotTo(HaveOccurred())
		Expect(pin.verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{parse(newCert)}})).To(Succeed())
		Expect(IsMaintenancePinMismatch(pin.verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{parse(oldCert)}}))).To(BeTrue())
	})

	It("leaves a pending machine alone when a different identity answers at its address", func() {
		cluster := clusterAt("refresh-other-cluster", "198.51.100.32")
		machineAt(cluster, "refresh-other", "198.51.100.32", "S-other", ichorv1alpha1.MachineApprovalStatePending)

		recorder := record.NewFakeRecorder(10)
		// A different UUID first (another machine behind the address), then the same UUID with another serial.
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder,
			MaintenanceClient: maintenanceFactory(node("refresh-stranger", "S-stranger"))}

		before := getMachine(tmNamespace, "refresh-other")

		found, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty())
		Expect(getMachine(tmNamespace, "refresh-stranger")).To(BeNil(), "no second machine is created at a known address")

		r.MaintenanceClient = maintenanceFactory(node("refresh-other", "S-different-serial"))
		found, err = r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty())

		after := getMachine(tmNamespace, "refresh-other")
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
		Expect(after.Status.MaintenanceFingerprint).To(Equal(oldFP))
		Consistently(recorder.Events).ShouldNot(Receive())
	})

	It("does not probe the address of an approved or rejected machine", func() {
		cluster := clusterAt("refresh-approved-cluster", "198.51.100.33", "198.51.100.34")
		machineAt(cluster, "refresh-approved", "198.51.100.33", "S-approved", ichorv1alpha1.MachineApprovalStateApproved)
		machineAt(cluster, "refresh-rejected", "198.51.100.34", "S-rejected2", ichorv1alpha1.MachineApprovalStateRejected)

		var (
			mu     sync.Mutex
			probed []string
		)

		factory := func(_ context.Context, endpoint string) (MaintenanceClient, error) {
			mu.Lock()
			probed = append(probed, endpoint)
			mu.Unlock()

			return node("refresh-approved", "S-approved"), nil
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(probed).To(BeEmpty())
		Expect(getMachine(tmNamespace, "refresh-approved").Status.MaintenanceFingerprint).To(Equal(oldFP))
		Expect(getMachine(tmNamespace, "refresh-rejected").Status.MaintenanceFingerprint).To(Equal(oldFP))
	})

	It("does not probe a pending machine's address that a Node of the cluster has", func() {
		cluster := clusterAt("refresh-node-cluster", "198.51.100.35")
		machineAt(cluster, "refresh-member", "198.51.100.35", "S-member", ichorv1alpha1.MachineApprovalStatePending)

		n := &corev1.Node{}
		n.Name = "refresh-member-node"
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })

		n.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "198.51.100.35"}}
		Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())

		var probed atomic.Int32

		factory := func(context.Context, string) (MaintenanceClient, error) {
			probed.Add(1)

			return node("refresh-member", "S-member"), nil
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(probed.Load()).To(BeZero())
	})

	It("writes nothing and records no Event when the pending machine answers with the certificate it pinned", func() {
		cluster := clusterAt("refresh-same-cluster", "198.51.100.36")
		machineAt(cluster, "refresh-same", "198.51.100.36", "S-same", ichorv1alpha1.MachineApprovalStatePending)

		fake := node("refresh-same", "S-same")
		fake.fingerprint = oldFP
		fake.inventory = Inventory{Disks: oldDisks}

		recorder := record.NewFakeRecorder(10)
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: maintenanceFactory(fake)}

		before := getMachine(tmNamespace, "refresh-same")

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(getMachine(tmNamespace, "refresh-same").ResourceVersion).To(Equal(before.ResourceVersion))
		Consistently(recorder.Events).ShouldNot(Receive())
	})
})
