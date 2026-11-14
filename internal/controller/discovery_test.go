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
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// fakeMaintenanceClient is the maintenance-mode Talos API boundary test
// double, the discovery equivalent of fakeTalosClient: no test here ever
// dials a real node (unit rule 2).
type fakeMaintenanceClient struct {
	identity HardwareIdentity
	err      error

	// applyErr and applyCalls serve talosmachine_provision_test.go.
	applyErr   error
	applyCalls [][]byte
	// applyHook runs inside ApplyConfiguration: another reconcile arriving while this one holds the cluster lock.
	applyHook func()

	// fingerprint is the maintenance certificate the fake claims to have seen (PeerFingerprinter).
	fingerprint string

	// inventory and inventoryErr are what the disk and NIC read returns.
	inventory    Inventory
	inventoryErr error

	// securityState and securityErr are what the maintenance-mode SecurityState read returns.
	securityState SecurityState
	securityErr   error
}

func (f *fakeMaintenanceClient) SecurityState(context.Context) (SecurityState, error) {
	return f.securityState, f.securityErr
}

func (f *fakeMaintenanceClient) TalosVersion(context.Context) (string, error) { return "", nil }

func (f *fakeMaintenanceClient) PeerFingerprint() string { return f.fingerprint }

func (f *fakeMaintenanceClient) Identity(context.Context) (HardwareIdentity, error) {
	return f.identity, f.err
}

func (f *fakeMaintenanceClient) Inventory(context.Context) (Inventory, error) {
	return f.inventory, f.inventoryErr
}

func (f *fakeMaintenanceClient) ApplyConfiguration(_ context.Context, cfg []byte) error {
	f.applyCalls = append(f.applyCalls, cfg)

	if f.applyHook != nil {
		f.applyHook()
	}

	return f.applyErr
}

func maintenanceFactory(fake *fakeMaintenanceClient) func(context.Context, string) (MaintenanceClient, error) {
	return func(context.Context, string) (MaintenanceClient, error) {
		return fake, nil
	}
}

// probeRecorder is a MaintenanceClient factory that records every endpoint it is asked for and hands out client.
type probeRecorder struct {
	client    MaintenanceClient
	mu        sync.Mutex
	endpoints []string
}

func (p *probeRecorder) factory(_ context.Context, endpoint string) (MaintenanceClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.endpoints = append(p.endpoints, endpoint)

	return p.client, nil
}

// createNode creates a Node with the InternalIP ip and the given conditions, deleted after the spec.
func createNode(name, ip string, annotations map[string]string, conds ...corev1.NodeCondition) {
	node := &corev1.Node{}
	node.Name, node.Annotations = name, annotations
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

	node.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}
	node.Status.Conditions = conds
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
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
	uuidMixed     = "60606060-6060-6060-6060-606060606060"
	uuidRejected  = "70707070-7070-7070-7070-707070707070"
	uuidMoved     = "90909090-9090-9090-9090-909090909090"

	// testMAC is the permanent MAC address every test below that doesn't
	// care about its specific value reuses.
	testMAC = "aa:bb:cc:dd:ee:ff"

	// testCIDR is RFC 5737's TEST-NET-1, reused by every EnumerateCandidates/DiscoverAll test
	// that needs a full /24 -- documentation-only address space, never a real network.
	testCIDR = "192.0.2.0/24"

	// testAddr is one address out of testCIDR, reused by tests that only need a single candidate.
	testAddr = "192.0.2.1"

	pinnedFingerprint = "pinned"
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

var _ = Describe("configuredNodeRejection", func() {
	DescribeTable("tells a configured node's rejection from an unreachable one",
		func(err error, want bool) {
			Expect(configuredNodeRejection(err)).To(Equal(want))
		},
		Entry("no error", nil, false),
		Entry("TLS alert after the handshake", status.Error(codes.Unavailable,
			`connection error: desc = "error reading server preface: remote error: tls: certificate required"`), true),
		Entry("bad certificate alert", status.Error(codes.Unavailable, "remote error: tls: bad certificate"), true),
		Entry("unauthenticated", status.Error(codes.Unauthenticated, "no client cert"), true),
		Entry("permission denied", status.Error(codes.PermissionDenied, "denied"), true),
		Entry("nothing listening", status.Error(codes.Unavailable, "connect: connection refused"), false),
		Entry("not TLS", status.Error(codes.Unavailable, "tls: first record does not look like a TLS handshake"), false),
		Entry("plain error", errors.New("boom"), false),
	)
})

var _ = Describe("DiscoveryReconciler.Discover already configured", func() {
	It("records an AlreadyConfigured Event with the endpoint and creates nothing", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		recorder := record.NewFakeRecorder(10)
		fake := &fakeMaintenanceClient{err: fmt.Errorf("%w: rejected", ErrAlreadyConfigured)}
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.60:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil())
		Expect(recorder.Events).To(Receive(And(
			ContainSubstring(ReasonAlreadyConfigured), ContainSubstring("10.0.0.60:50000"))))
	})
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
		}, fingerprint: "OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o=",
			inventory: Inventory{
				Disks: []ichorv1alpha1.TalosMachineDiscoveredDisk{{Serial: "D1", DevPath: inventoryDisk, SizeBytes: 1 << 30}},
				Links: []ichorv1alpha1.TalosMachineDiscoveredLink{{Name: inventoryNIC, PermanentMAC: testMAC, LinkUp: true}},
			},
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.21:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).NotTo(BeNil())

		Expect(m.Name).To(Equal(uuidCreate))
		Expect(m.Spec.IsApproved()).To(BeFalse())
		Expect(m.Spec.Role).To(BeEmpty())
		Expect(m.Spec.Endpoint).To(Equal("10.0.0.21:50000"))
		Expect(m.Spec.Identity).To(Equal(ichorv1alpha1.TalosMachineIdentity{
			UUID: uuidCreate, Serial: "S1", MACAddresses: []string{testMAC},
		}))

		got := getMachine(cluster.Namespace, uuidCreate)
		Expect(got).NotTo(BeNil())
		Expect(got.Status.Conditions).To(BeEmpty())
		// The certificate the node presented is pinned for the first configuration.
		Expect(got.Status.MaintenanceFingerprint).To(Equal("OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o="))
		// The disks and NICs the probe read are recorded for approval to choose from.
		Expect(got.Status.DiscoveredDisks).To(Equal(fake.inventory.Disks))
		Expect(got.Status.DiscoveredLinks).To(Equal(fake.inventory.Links))
	})

	It("still registers a pending machine when the disk and NIC read fails", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		const uuidInventoryFail = "a0a0a0a0-a0a0-a0a0-a0a0-a0a0a0a0a0a0"
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidInventoryFail, Serial: "S9", MACAddresses: []string{testMAC},
		}, inventoryErr: errors.New("permission denied")}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.29:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).NotTo(BeNil())

		got := getMachine(cluster.Namespace, uuidInventoryFail)
		Expect(got).NotTo(BeNil())
		Expect(got.Status.DiscoveredDisks).To(BeEmpty())
		Expect(got.Status.DiscoveredLinks).To(BeEmpty())
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

	It("records the new address in status, never in spec, when the same machine answers somewhere else", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidMoved, Serial: "S9", MACAddresses: []string{"11:22:33:44:55:99"},
		}}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		_, err := r.Discover(ctx, cluster, "10.0.0.30:50000")
		Expect(err).NotTo(HaveOccurred())

		moved, err := r.Discover(ctx, cluster, "10.0.0.31:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(moved).NotTo(BeNil())

		got := getMachine(cluster.Namespace, uuidMoved)
		Expect(got.Spec.Endpoint).To(Equal("10.0.0.30:50000"), "the controller never mutates spec")
		Expect(got.Status.ObservedEndpoint).To(Equal("10.0.0.31:50000"))
		Expect(got.DialEndpoint()).To(Equal("10.0.0.31:50000"))

		// Seen again at the same place: nothing to write.
		again, err := r.Discover(ctx, cluster, "10.0.0.31:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ResourceVersion).To(Equal(got.ResourceVersion))
	})

	// A node that answers the insecure maintenance API with an approved machine's identity must not
	// move where an approved machine is dialled once the machine has, or is getting, its
	// configuration: the identity is self-reported, and what follows a redirect is the CA keys (and an
	// etcd member removed) for a converged machine that lost its disk. Only a machine that never
	// received its configuration and whose maintenance certificate was pinned is still followed:
	// the pin refuses any other certificate at the new address.
	DescribeTable("an approved machine seen at another address",
		func(uuid string, status ichorv1alpha1.TalosMachineStatus, wantMoved bool) {
			cluster := newTestCluster("does-not-exist", "v1.14")
			id := ichorv1alpha1.TalosMachineIdentity{UUID: uuid, Serial: "S-approved", MACAddresses: []string{"11:22:33:44:55:aa"}}
			m := &ichorv1alpha1.TalosMachine{
				Name: uuid, Namespace: cluster.Namespace,
				Spec: ichorv1alpha1.TalosMachineSpec{
					ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: id,
					Endpoint: "10.0.1.30:50000", ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
				},
			}
			Expect(k8sClient.Create(ctx, m)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, m) })

			m.Status = status
			Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

			fake := &fakeMaintenanceClient{identity: HardwareIdentity{UUID: uuid, Serial: id.Serial, MACAddresses: id.MACAddresses}}
			r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

			_, err := r.Discover(ctx, cluster, "10.0.1.99:50000")
			Expect(err).NotTo(HaveOccurred())

			got := getMachine(cluster.Namespace, uuid)
			Expect(got.Spec.Endpoint).To(Equal("10.0.1.30:50000"))

			if wantMoved {
				Expect(got.DialEndpoint()).To(Equal("10.0.1.99:50000"))
			} else {
				Expect(got.Status.ObservedEndpoint).To(BeEmpty(), "a discovery match must not redirect an approved machine")
				Expect(got.DialEndpoint()).To(Equal("10.0.1.30:50000"))
			}
		},
		Entry("whose configuration it applied is not moved", "a1000000-0000-0000-0000-000000000001",
			ichorv1alpha1.TalosMachineStatus{AppliedConfigHash: "sha256:applied", MaintenanceFingerprint: pinnedFingerprint}, false),
		Entry("whose configuration was stored (re-provisioning) is not moved", "a1000000-0000-0000-0000-000000000002",
			ichorv1alpha1.TalosMachineStatus{ConfigStoredAt: ptr.To(metav1.Now()), MaintenanceFingerprint: pinnedFingerprint}, false),
		Entry("that never got a configuration and has no pinned certificate is not moved", "a1000000-0000-0000-0000-000000000003",
			ichorv1alpha1.TalosMachineStatus{}, false),
		Entry("that never got a configuration and has a pinned certificate is followed", "a1000000-0000-0000-0000-000000000004",
			ichorv1alpha1.TalosMachineStatus{MaintenanceFingerprint: pinnedFingerprint}, true),
	)

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

	It("returns a rejected machine unchanged rather than recreating it as fresh and pending", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")

		rejected := &ichorv1alpha1.TalosMachine{
			Name: uuidRejected, Namespace: cluster.Namespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
				Identity: ichorv1alpha1.TalosMachineIdentity{
					UUID: uuidRejected, Serial: "S-rejected", MACAddresses: []string{"cc:cc:cc:cc:cc:cc"},
				},
				Endpoint:      "10.0.0.40:50000",
				ApprovalState: ichorv1alpha1.MachineApprovalStateRejected,
			},
		}
		Expect(k8sClient.Create(ctx, rejected)).To(Succeed())

		// The same hardware presenting itself again -- identical serial and
		// MAC, same UUID -- is exactly the "same machine returning" branch:
		// a reject must not be undone by the node simply existing on the
		// network, or RejectMachine's whole point (stopping it from showing
		// up as a fresh approval request) would be defeated the next time
		// discovery runs.
		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidRejected, Serial: "S-rejected", MACAddresses: []string{"cc:cc:cc:cc:cc:cc"},
		}}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		m, err := r.Discover(ctx, cluster, "10.0.0.40:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).NotTo(BeNil())

		Expect(m.Name).To(Equal(rejected.Name))
		Expect(m.ResourceVersion).To(Equal(rejected.ResourceVersion), "Discover must not write to an existing machine it only looked up")
		Expect(m.Spec.ApprovalState).To(Equal(ichorv1alpha1.MachineApprovalStateRejected))

		got := getMachine(cluster.Namespace, uuidRejected)
		Expect(got.Spec.ApprovalState).To(Equal(ichorv1alpha1.MachineApprovalStateRejected))
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
		reconcileOK(newTestReconciler(talosFake), m)

		Expect(talosFake.dryRunCalls).To(BeZero())
		Expect(talosFake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.Conditions).To(BeEmpty())
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())
	})
})

// None of the tests below ever construct the real MaintenanceClient
// (NewMachineryMaintenanceClient): every MaintenanceClient factory here is
// a fake that cannot, even by accident, open a socket. That matters more
// than usual for this file -- EnumerateCandidates and DiscoverAll exist
// specifically to turn declared ranges into connection attempts, on a
// workstation that can also reach a real network.
var _ = Describe("EnumerateCandidates", func() {
	It("returns nothing when discovery is not declared at all", func() {
		Expect(EnumerateCandidates(&ichorv1alpha1.TalosCluster{})).To(BeEmpty())
	})

	It("pairs each address with Talos's fixed maintenance port", func() {
		cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.10", "192.0.2.11"}},
		}}

		Expect(EnumerateCandidates(cluster)).To(ConsistOf("192.0.2.10:50000", "192.0.2.11:50000"))
	})

	It("expands a /24 CIDR to every address in it, same fixed port", func() {
		cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{CIDRs: []string{testCIDR}},
		}}

		got := EnumerateCandidates(cluster)
		Expect(got).To(HaveLen(256))
		Expect(got).To(ContainElement("192.0.2.0:50000"))
		Expect(got).To(ContainElement("192.0.2.255:50000"))
	})

	It("combines addresses and cidrs", func() {
		cluster := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{
				Addresses: []string{"198.51.100.5"},
				CIDRs:     []string{testCIDR},
			},
		}}

		Expect(EnumerateCandidates(cluster)).To(HaveLen(257))
	})
})

var _ = Describe("DiscoveryReconciler.DiscoverAll", func() {
	It("opens no connection at all when discovery is not declared (the default)", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "no-discovery"}

		var calls atomic.Int32
		factory := func(context.Context, string) (MaintenanceClient, error) {
			calls.Add(1)

			return nil, errors.New("must never be called")
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		results, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
		Expect(calls.Load()).To(BeZero())
	})

	It("never runs more than discoveryConcurrency connection attempts at once", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "concurrency", Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{CIDRs: []string{testCIDR}}, // 256 candidates
		}}

		var inFlight, maxInFlight atomic.Int32
		factory := func(context.Context, string) (MaintenanceClient, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)

			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}

			time.Sleep(5 * time.Millisecond) // simulated work only -- never a real dial.

			return nil, errors.New("simulated unreachable")
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(int(maxInFlight.Load())).To(BeNumerically("<=", discoveryConcurrency))
	})

	It("does not hang on an address that never answers", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "slow", Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr, "192.0.2.2"}},
		}}

		factory := func(ctx context.Context, _ string) (MaintenanceClient, error) {
			<-ctx.Done() // a real dial against a silent address blocks the same way.

			return nil, ctx.Err()
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		start := time.Now()
		results, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
		Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second))
	})

	It("records nothing -- no event, no error -- for an address that fails to connect", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "silent", Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr}},
		}}

		recorder := record.NewFakeRecorder(10)
		factory := func(context.Context, string) (MaintenanceClient, error) {
			return nil, errors.New("connection refused")
		}

		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: factory}

		results, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
		Consistently(recorder.Events).ShouldNot(Receive())
	})

	It("returns the machines it does discover alongside many that never answer", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "mixed", Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr, "192.0.2.2", "192.0.2.3"}},
		}}

		const reachable = "192.0.2.2:50000"
		factory := func(_ context.Context, endpoint string) (MaintenanceClient, error) {
			if endpoint != reachable {
				return nil, errors.New("connection refused")
			}

			return &fakeMaintenanceClient{identity: HardwareIdentity{
				UUID: uuidMixed, Serial: "S1", MACAddresses: []string{testMAC},
			}}, nil
		}

		r := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory}

		results, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal(uuidMixed))
	})
})

var _ = Describe("DiscoveryReconciler.DiscoverAll known addresses", func() {
	It("skips the addresses of the cluster's machines and Nodes, and still reports an unknown configured node", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "skip-known", Spec: ichorv1alpha1.TalosClusterSpec{
			Discovery: &ichorv1alpha1.TalosClusterDiscovery{
				Addresses: []string{"198.51.100.10", "198.51.100.11", "198.51.100.12", "198.51.100.13"},
			},
		}}

		// .10 is an approved machine's spec.endpoint, .11 only its status.observedEndpoint. (A pending
		// machine's addresses are probed, to refresh it: see "DiscoverAll pending machines".)
		m := &ichorv1alpha1.TalosMachine{
			Name: "skip-known-machine", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "skip-known-machine", Serial: "S1", MACAddresses: []string{testMAC}},
				Endpoint:      "198.51.100.10:50000",
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, m) })

		m.Status.ObservedEndpoint = "198.51.100.11:50000"
		m.Status.ObservedEndpointFor = m.Spec.Endpoint
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		// .12 matches only a Node's InternalIP.
		createNode("skip-known-node", "198.51.100.12", nil)

		probes := &probeRecorder{client: &fakeMaintenanceClient{err: fmt.Errorf("%w: rejected", ErrAlreadyConfigured)}}
		recorder := record.NewFakeRecorder(10)
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: probes.factory}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(probes.endpoints).To(ConsistOf("198.51.100.13:50000"))
		Expect(recorder.Events).To(Receive(ContainSubstring("198.51.100.13:50000")))
		Consistently(recorder.Events).ShouldNot(Receive())
	})

	It("probes the address of a Node that a reset by Ichor left behind, but not once it is Ready again", func() {
		cluster := &ichorv1alpha1.TalosCluster{Name: "reset-node-cluster", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosClusterSpec{Discovery: &ichorv1alpha1.TalosClusterDiscovery{
				Addresses: []string{"198.51.100.20", "198.51.100.21"},
			}}}

		reset := map[string]string{nodeResetByIchorAnnotation: "2026-11-02T00:00:00Z"}
		createNode("reset-left-node", "198.51.100.20", reset, corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionUnknown})
		createNode("reset-rejoined-node", "198.51.100.21", reset, corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue})

		probes := &probeRecorder{client: &fakeMaintenanceClient{err: fmt.Errorf("%w: rejected", ErrAlreadyConfigured)}}
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: record.NewFakeRecorder(10), MaintenanceClient: probes.factory}

		_, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(probes.endpoints).To(ConsistOf("198.51.100.20:50000"))
	})
})

var _ = Describe("DiscoverAll orphans", func() {
	nodeWithUUID := func(name, ip, uuid string) {
		node := &corev1.Node{}
		node.Name = name
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		node.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}
		node.Status.NodeInfo.SystemUUID = uuid
		Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
	}

	It("probes a Node no TalosMachine accounts for and reports a configured node there as Orphan, not AlreadyConfigured", func() {
		cluster := &ichorv1alpha1.TalosCluster{Name: "orphan-cluster", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosClusterSpec{Discovery: &ichorv1alpha1.TalosClusterDiscovery{
				Addresses: []string{"198.51.100.30", "198.51.100.31", "198.51.100.32"},
			}}}

		// .30: a Node of a member (its TalosMachine has the UUID, a stale endpoint); .31: the orphan;
		// .32: no Node at all (a foreign device).
		m := &ichorv1alpha1.TalosMachine{
			Name: "orphan-member-machine", Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "orphan-member-uuid", Serial: "S1", MACAddresses: []string{testMAC}},
				Endpoint:      "198.51.100.99:50000",
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, m) })

		nodeWithUUID("orphan-member-node", "198.51.100.30", "ORPHAN-MEMBER-UUID")
		nodeWithUUID("orphan-lost-node", "198.51.100.31", "orphan-lost-uuid")

		probes := &probeRecorder{client: &fakeMaintenanceClient{err: fmt.Errorf("%w: rejected", ErrAlreadyConfigured)}}
		recorder := record.NewFakeRecorder(10)
		r := &DiscoveryReconciler{Client: k8sClient, Recorder: recorder, MaintenanceClient: probes.factory}

		found, err := r.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty())
		Expect(probes.endpoints).To(ConsistOf("198.51.100.31:50000", "198.51.100.32:50000"))

		var events []string
		for len(recorder.Events) > 0 {
			events = append(events, <-recorder.Events)
		}

		Expect(events).To(ConsistOf(
			And(ContainSubstring(ReasonOrphan), ContainSubstring("198.51.100.31:50000")),
			And(ContainSubstring(ReasonAlreadyConfigured), ContainSubstring("198.51.100.32:50000")),
		))
	})
})
