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
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const uuidClusterReconcile = "80808080-8080-8080-8080-808080808080"

var errFactoryMustNotBeCalled = errors.New("must never be called")

func reconcileCluster(r *TalosClusterReconciler, c *ichorv1alpha1.TalosCluster) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: c.Namespace, Name: c.Name})
}

var _ = Describe("TalosClusterReconciler", func() {
	It("does not requeue, and never opens a connection, when spec.discovery is unset (the default)", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")

		var calls atomic.Int32
		factory := func(context.Context, string) (MaintenanceClient, error) {
			calls.Add(1)

			return nil, errFactoryMustNotBeCalled
		}

		r := &TalosClusterReconciler{
			Client:    k8sClient,
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory},
		}

		result, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
		Expect(calls.Load()).To(BeZero())
	})

	It("ignores a TalosCluster that no longer exists", func() {
		r := &TalosClusterReconciler{Client: k8sClient}

		result, err := r.Reconcile(ctx, ctrl.Request{Namespace: tmNamespace, Name: "no-such-cluster"})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
	})

	It("probes a declared range and requeues at discoveryRequeueInterval", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{testAddr}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		factory := func(_ context.Context, endpoint string) (MaintenanceClient, error) {
			return &fakeMaintenanceClient{identity: HardwareIdentity{
				UUID: uuidClusterReconcile, Serial: "S1", MACAddresses: []string{testMAC},
			}}, nil
		}

		r := &TalosClusterReconciler{
			Client:    k8sClient,
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory},
		}

		result, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(discoveryRequeueInterval))

		Expect(getMachine(cluster.Namespace, uuidClusterReconcile)).NotTo(BeNil())
	})

	It("scans a range added to spec.discovery.cidrs at the next reconcile, which the spec update itself triggers", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{CIDRs: []string{"10.9.9.0/30"}}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		var mu sync.Mutex

		var dialed []string

		factory := func(_ context.Context, endpoint string) (MaintenanceClient, error) {
			mu.Lock()
			dialed = append(dialed, endpoint)
			mu.Unlock()

			return nil, errFactoryMustNotBeCalled
		}

		r := &TalosClusterReconciler{
			Client:    k8sClient,
			Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory},
		}

		result, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(discoveryFreshInterval), "nothing answered and the range is new")

		mu.Lock()
		Expect(dialed).To(HaveLen(4))
		Expect(strings.Join(dialed, " ")).NotTo(ContainSubstring("10.9.8."))
		mu.Unlock()
	})
})
