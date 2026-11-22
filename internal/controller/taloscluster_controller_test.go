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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const uuidClusterReconcile = "80808080-8080-8080-8080-808080808080"

var errFactoryMustNotBeCalled = errors.New("must never be called")

func reconcileCluster(r *TalosClusterReconciler, c *ichorv1alpha1.TalosCluster) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: c.Namespace, Name: c.Name})
}

func newDiscoveryReconciler(factory func(context.Context, string) (MaintenanceClient, error)) *TalosClusterReconciler {
	return &TalosClusterReconciler{
		Client:    k8sClient,
		Discovery: &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: factory},
	}
}

var _ = Describe("TalosClusterReconciler", func() {
	It("does not requeue, and never opens a connection, when spec.discovery is unset (the default)", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")

		var calls atomic.Int32
		factory := func(context.Context, string) (MaintenanceClient, error) {
			calls.Add(1)

			return nil, errFactoryMustNotBeCalled
		}

		r := newDiscoveryReconciler(factory)

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

		r := newDiscoveryReconciler(factory)

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

		r := newDiscoveryReconciler(factory)

		result, err := reconcileCluster(r, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(discoveryFreshInterval), "nothing answered and the range is new")

		mu.Lock()
		Expect(dialed).To(HaveLen(4))
		Expect(strings.Join(dialed, " ")).NotTo(ContainSubstring("10.9.8."))
		mu.Unlock()
	})

	// A node Ichor reset is found again within seconds, not at the next five-minute pass: the
	// wiped machine answers a minute after the reset, long after the pass that saw it (2026-11-05).
	Context("after a node was reset by Ichor", func() {
		// resetCluster is a cluster whose discovery range has been known for longer than the fresh
		// window, so only the reset Node can make the pass short.
		resetCluster := func() (*TalosClusterReconciler, *ichorv1alpha1.TalosCluster, *time.Time) {
			cluster := newTestCluster("does-not-exist", "v1.14")
			cluster.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{CIDRs: []string{"10.9.7.0/30"}}
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

			clock := time.Now()
			factory := func(context.Context, string) (MaintenanceClient, error) { return nil, errFactoryMustNotBeCalled }
			r := newDiscoveryReconciler(factory)
			r.now = func() time.Time { return clock }

			_, err := reconcileCluster(r, cluster)
			Expect(err).NotTo(HaveOccurred())

			clock = clock.Add(2 * discoveryFreshWindow)

			return r, cluster, &clock
		}

		resetNode := func(resetAt time.Time) {
			node := &corev1.Node{GenerateName: "reset-node-", Annotations: map[string]string{
				nodeResetByIchorAnnotation: resetAt.UTC().Format(time.RFC3339),
			}}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, node)).To(Succeed()) })
		}

		It("looks again every discoveryFreshInterval for discoveryFreshWindow after the reset", func() {
			r, cluster, clock := resetCluster()
			resetNode(clock.Add(-discoveryFreshWindow / 2))

			result, err := reconcileCluster(r, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(discoveryFreshInterval))
		})

		It("goes back to discoveryRequeueInterval once the window has passed", func() {
			r, cluster, clock := resetCluster()
			resetNode(clock.Add(-2 * discoveryFreshWindow))

			result, err := reconcileCluster(r, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(discoveryRequeueInterval))
		})

		It("watches only the Nodes that carry the reset marker", func() {
			marked := &corev1.Node{Annotations: map[string]string{nodeResetByIchorAnnotation: "x"}}
			Expect(isResetNode(marked)).To(BeTrue())
			Expect(isResetNode(&corev1.Node{})).To(BeFalse())
		})
	})
})

var _ = Describe("TalosClusterReconciler kube-proxy consent sweep", func() {
	const switched = "replaced"

	// consentCluster is a cluster whose spec and consent annotation name mode and whose status
	// says the switch finished (statusMode == mode) or has not run (statusMode empty).
	consentCluster := func(mode, statusMode string) *ichorv1alpha1.TalosCluster {
		cluster := newTestCluster("consent-sweep", "v1.14")
		cluster.Spec.CNI.KubeProxy = mode
		cluster.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: mode}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		if statusMode != "" {
			cluster.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: statusMode}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())
		}

		return cluster
	}

	consentLeft := func(c *ichorv1alpha1.TalosCluster) bool {
		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(c), &got)).To(Succeed())

		_, ok := got.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation]

		return ok
	}

	sweep := func(c *ichorv1alpha1.TalosCluster) {
		GinkgoHelper()

		_, err := reconcileCluster(&TalosClusterReconciler{Client: k8sClient}, c)
		Expect(err).NotTo(HaveOccurred())
	}

	It("removes a consent left behind once the switch has finished and no operation runs", func() {
		cluster := consentCluster(switched, switched)

		sweep(cluster)

		Expect(consentLeft(cluster)).To(BeFalse())
	})

	It("keeps the consent while the switch is pending (Git first: the spec leads the status)", func() {
		cluster := consentCluster(switched, "")

		sweep(cluster)

		Expect(consentLeft(cluster)).To(BeTrue())
	})

	It("keeps the consent while a kube-proxy switch operation has not finished, even after the status moved", func() {
		cluster := consentCluster(switched, switched)
		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindCNI)

		sweep(cluster)
		Expect(consentLeft(cluster)).To(BeTrue(), "the operation is Pending")

		op.Status.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
		Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())

		sweep(cluster)
		Expect(consentLeft(cluster)).To(BeFalse())
	})
})
