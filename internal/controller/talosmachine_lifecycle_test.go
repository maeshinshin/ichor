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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	uuidRemoved = "a1a1a1a1-a1a1-a1a1-a1a1-a1a1a1a1a1a1"
	uuidRaced   = "b2b2b2b2-b2b2-b2b2-b2b2-b2b2b2b2b2b2"
	uuidDHCP    = "c3c3c3c3-c3c3-c3c3-c3c3-c3c3c3c3c3c3"
)

var _ = Describe("the TalosMachine lifecycle around removal and discovery", func() {
	// ADR 0135: a removal deletes the object; the wiped node, back in maintenance mode, is found as a NEW
	// pending machine of the same name. Two objects, two UIDs: a waiter for "the object disappears" must
	// compare UIDs, not names.
	It("deletes the removed machine, and discovery then creates a new pending one under the same name", func() {
		r, secretName := removalReadyReconciler()
		cluster := newTestClusterWithCount(1, secretName)

		cp1 := &ichorv1alpha1.TalosMachine{
			Name: uuidRemoved, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: uuidRemoved, Serial: "S-removed", MACAddresses: []string{testMAC}},
				Endpoint:      "10.0.2.91:50000",
				Role:          roleControlPlane,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, cp1)).To(Succeed())

		cp2, cp3 := newTestControlPlane(cluster.Name, "92"), newTestControlPlane(cluster.Name, "93")
		reconcileOK(r, cp1, cp2, cp3)

		// Positive control: before the removal the object is the approved control plane.
		before := fetchMachine(cp1)
		Expect(before.Spec.Role).To(Equal(roleControlPlane))
		Expect(before.Spec.IsApproved()).To(BeTrue())

		Expect(k8sClient.Delete(ctx, cp1)).To(Succeed())
		reconcileOK(r, cp1)
		Expect(machineGone(cp1)).To(BeTrue(), "the removal clears its finalizer and the object is deleted")

		fake := &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: uuidRemoved, Serial: "S-removed", MACAddresses: []string{testMAC},
		}}
		d := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		found, err := d.Discover(ctx, cluster, "10.0.2.91:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).NotTo(BeNil())

		after := fetchMachine(cp1)
		Expect(after.Name).To(Equal(before.Name))
		Expect(after.UID).NotTo(Equal(before.UID), "a new object, not the removed one")
		Expect(after.Spec.IsApproved()).To(BeFalse())
		Expect(after.Spec.Role).To(BeEmpty())
	})

	// A pending machine whose node came back at another address (a DHCP lease, a pod IP) is followed through
	// status.observedEndpoint, found by a sweep whose range holds only the new address; spec is untouched.
	It("follows a pending machine whose node moved, found by a sweep of the new address alone", func() {
		cluster := &ichorv1alpha1.TalosCluster{Namespace: tmNamespace, Name: "dhcp-cluster",
			Spec: ichorv1alpha1.TalosClusterSpec{Discovery: &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"198.51.100.61"}}}}
		m := &ichorv1alpha1.TalosMachine{
			Name: uuidDHCP, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: uuidDHCP, Serial: "S-dhcp", MACAddresses: []string{testMAC}},
				Endpoint:      "198.51.100.60:50000",
				ApprovalState: ichorv1alpha1.MachineApprovalStatePending,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, m) })

		fake := &fakeMaintenanceClient{identity: HardwareIdentity{UUID: uuidDHCP, Serial: "S-dhcp", MACAddresses: []string{testMAC}}}
		d := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: maintenanceFactory(fake)}

		_, err := d.DiscoverAll(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		got := getMachine(tmNamespace, uuidDHCP)
		Expect(got.Spec.Endpoint).To(Equal("198.51.100.60:50000"), "the controller never mutates spec")
		Expect(got.Status.ObservedEndpoint).To(Equal("198.51.100.61:50000"))
		Expect(got.DialEndpoint()).To(Equal("198.51.100.61:50000"), "no annotation or delete is needed to follow the node")
	})

	// init (or another discovery pass) may create the machine between discovery's Get and its Create. The
	// loser adopts what is there: nothing is overwritten and no error reaches the sweep.
	It("leaves the machine another writer created first untouched, and reports no error", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		first := &ichorv1alpha1.TalosMachine{
			Name: uuidRaced, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: cluster.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: uuidRaced, Serial: "S-raced", MACAddresses: []string{testMAC}},
				Endpoint:      "10.0.3.1:50000",
				Role:          roleControlPlane,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, first)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, first) })

		// The Get discovery does before its Create has not seen the object yet.
		stale := interceptor.NewClient(watchClient(), interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*ichorv1alpha1.TalosMachine); ok && key.Name == uuidRaced {
					return apierrors.NewNotFound(schema.GroupResource{Group: "ichor.maesh.dev", Resource: "talosmachines"}, key.Name)
				}

				return c.Get(ctx, key, obj, opts...)
			},
		})

		fake := &fakeMaintenanceClient{identity: HardwareIdentity{UUID: uuidRaced, Serial: "S-raced", MACAddresses: []string{testMAC}}}
		d := &DiscoveryReconciler{Client: stale, MaintenanceClient: maintenanceFactory(fake)}

		m, err := d.Discover(ctx, cluster, "10.0.3.1:50000")
		Expect(err).NotTo(HaveOccurred())
		Expect(m).To(BeNil(), "nothing was created: the machine another writer made is left as it is")

		got := getMachine(tmNamespace, uuidRaced)
		Expect(got.Spec.Role).To(Equal(roleControlPlane))
		Expect(got.Spec.IsApproved()).To(BeTrue())
	})
})
