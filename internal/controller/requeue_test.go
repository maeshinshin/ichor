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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// newBundledMachine is a cluster with its secrets bundle and one approved machine of it.
func newBundledMachine() (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) {
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)
	cluster := newTestCluster(secret.Name, "v1.14")

	return cluster, newTestMachine(cluster.Name, true), bundle
}

// A False Ready that time can fix must come back by itself; one that waits for an operator must not.
var _ = Describe("TalosMachineReconciler requeue after a failure", func() {
	It("retries on its own once the node answers again, without another event", func() {
		cluster, m, bundle := newBundledMachine()
		current, hash, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		reachable := false
		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
				if !reachable {
					return nil, errors.New("dial refused")
				}

				return fake, nil
			},
		}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonTalosConnectFailed))

		reachable = true
		res, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(reachabilityRecheck), "a converged machine is re-probed, so a lost node shows in Ready")

		got := fetchMachine(m)
		Expect(readyCondition(got).Reason).To(Equal(reasonConverged))
		Expect(got.Status.AppliedConfigHash).To(Equal(hash))
	})

	It("follows a node whose address changed: records the Kubernetes Node's address in status, never in spec", func() {
		_, m, _ := newBundledMachine()
		setHostname(m, "node-moved-1")

		m = fetchMachine(m)
		m.Status.AppliedConfigHash = "sha256:previous"
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		node := &corev1.Node{}
		node.Name = "node-moved-1"
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		node.Status.Addresses = []corev1.NodeAddress{
			{Type: corev1.NodeHostName, Address: "node-moved-1"}, {Type: corev1.NodeInternalIP, Address: "10.9.9.9"},
		}
		Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

		var dialed []string
		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, mm *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
				dialed = append(dialed, mm.DialEndpoint())

				return &fakeTalosClient{currentErr: errors.New("i/o timeout")}, nil
			},
		}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).NotTo(BeZero())

		got := fetchMachine(m)
		Expect(got.Spec.Endpoint).To(Equal(storageTestEndpoint), "the controller never mutates spec")
		Expect(got.Status.ObservedEndpoint).To(Equal("10.9.9.9:50000"))

		reconcileOK(r, m)
		Expect(dialed).To(Equal([]string{storageTestEndpoint, "10.9.9.9:50000"}), "the next round dials the observed address")

		// An operator editing spec.endpoint overrides what was observed.
		got = fetchMachine(m)
		got.Spec.Endpoint = "10.0.0.77:50000"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(got.DialEndpoint()).To(Equal("10.0.0.77:50000"))
	})

	It("asks to be looked at again when the node's current configuration cannot be read", func() {
		_, m, _ := newBundledMachine()
		fake := &fakeTalosClient{currentErr: errors.New("read failed")}
		res, err := reconcileMachine(newTestReconciler(fake), m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonCurrentConfigUnavailable))
	})

	It("reports NodeUnreachable with the endpoint and the dial error once reads fail past the grace, and suggests the Node's new address", func() {
		cluster, m, bundle := newBundledMachine()
		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := newTestReconciler(fake)

		reconcileOK(r, m)
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonConverged))

		// The node's IP changed: reads now fail. Within the grace the usual reason stays.
		fake.currentErr = errors.New("dial tcp " + m.Spec.Endpoint + ": i/o timeout")
		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonCurrentConfigUnavailable))

		// Past the grace (the failure began longer ago), Ready says why, and spec is untouched.
		got := fetchMachine(m)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionNodeReachable)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * nodeUnreachableGrace))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		reconcileOK(r, m)

		got = fetchMachine(m)
		ready := readyCondition(got)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(reasonNodeUnreachable))
		Expect(ready.Message).To(ContainSubstring(m.Spec.Endpoint))
		Expect(ready.Message).To(ContainSubstring("i/o timeout"))
		Expect(ready.Message).NotTo(ContainSubstring("spec.endpoint may need updating"), "no Node, no suggestion")
		Expect(got.Spec.Endpoint).To(Equal(m.Spec.Endpoint))

		// The machine's Kubernetes Node (matched by its UUID) reports another address: suggested, not applied.
		node := &corev1.Node{}
		node.Name = "node-new-ip-1"
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, node) })

		node.Status.NodeInfo.SystemUUID = m.Spec.Identity.UUID
		node.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.77.7.7"}}
		Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())
		Eventually(func() string {
			var n corev1.Node
			_ = k8sClient.Get(ctx, client.ObjectKeyFromObject(node), &n)

			return n.Status.NodeInfo.SystemUUID
		}).Should(Equal(m.Spec.Identity.UUID))

		reconcileOK(r, m)

		got = fetchMachine(m)
		Expect(readyCondition(got).Message).To(ContainSubstring("spec.endpoint may need updating to 10.77.7.7:50000"))
		Expect(got.Spec.Endpoint).To(Equal(m.Spec.Endpoint), "the controller never mutates spec")

		// The node answers again: converged, and the unreachable marker is gone.
		fake.currentErr = nil
		reconcileOK(r, m)

		got = fetchMachine(m)
		Expect(readyCondition(got).Reason).To(Equal(reasonConverged))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionNodeReachable)).To(BeNil())
	})

	It("reports a worker whose endpoint answers as another machine as EndpointIdentityMismatch with both UUIDs", func() {
		cluster, m, bundle := newBundledMachine()
		Expect(m.Spec.Role).To(Equal(roleWorker))

		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current, identity: HardwareIdentity{UUID: m.Spec.Identity.UUID}}
		r := newTestReconciler(fake)

		reconcileOK(r, m)
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonConverged))

		// The old address is now held by another node: it answers, with the same configuration.
		fake.identity = HardwareIdentity{UUID: "other-machine-uuid"}
		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))

		got := fetchMachine(m)
		ready := readyCondition(got)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(reasonEndpointIdentityMismatch))
		Expect(ready.Message).To(ContainSubstring("other-machine-uuid"))
		Expect(ready.Message).To(ContainSubstring(m.Spec.Identity.UUID))
		Expect(got.Spec.Endpoint).To(Equal(m.Spec.Endpoint), "the controller never mutates spec")

		// The right node is back at the address: converged again.
		fake.identity = HardwareIdentity{UUID: m.Spec.Identity.UUID}
		reconcileOK(r, m)
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonConverged))
	})

	It("reports a worker that cannot be dialed as NodeUnreachable once the grace has passed", func() {
		cluster, m, bundle := newBundledMachine()
		Expect(m.Spec.Role).To(Equal(roleWorker))

		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := newTestReconciler(fake)

		reconcileOK(r, m)

		fake.currentErr = errors.New("dial tcp " + m.Spec.Endpoint + ": i/o timeout")
		reconcileOK(r, m)

		got := fetchMachine(m)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionNodeReachable)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * nodeUnreachableGrace))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		reconcileOK(r, m)
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonNodeUnreachable))
	})

	It("waits for the Secret watch, without polling, when the secrets bundle is missing", func() {
		cluster := newTestCluster("no-such-secret", "v1.14")
		m := newTestMachine(cluster.Name, true)
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonSecretsBundleUnavailable))
	})

	It("retries a failed apply, and applies once the node accepts it", func() {
		cluster, m, bundle := newBundledMachine()
		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())
		newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())

		fake := &fakeTalosClient{current: current, applyErr: errors.New("node busy")}
		r := newTestReconciler(fake)

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonApplyFailed))

		fake.applyErr = nil
		res, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(reachabilityRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonApplied))
	})

	It("does not poll for a change that needs a reboot", func() {
		cluster, m, bundle := newBundledMachine()
		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())
		disabled := true
		desired := minimalConfigSkeleton()
		desired.ClusterConfig.ProxyConfig = &typedconfig.ProxyConfig{Disabled: &disabled}
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current}
		res, err := reconcileMachine(newTestReconciler(fake), m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonRebootRequired))
	})

	It("does not poll for an install-time-only change", func() {
		cluster, m, bundle := newBundledMachine()
		target := &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: m.Name}}
		current := renderWithConfig(cluster, m, bundle, "machine", target, minimalConfigSkeleton())
		desired := minimalConfigSkeleton()
		wipe := true
		desired.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/sdb", InstallWipe: &wipe}
		newTestPatch(cluster.Name, "machine", target, desired)

		fake := &fakeTalosClient{current: current}
		res, err := reconcileMachine(newTestReconciler(fake), m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonInstallTimeOnlyChange))
	})

	It("does not poll for drift under driftPolicy warnOnly", func() {
		cluster, m, bundle := newBundledMachine()
		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		rendered, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: rendered}
		r := newTestReconciler(fake)
		reconcileOK(r, m)

		drifted := extraArgsConfig()
		drifted.ClusterConfig.APIServerConfig.ExtraArgsConfig["foo"] = []string{"c"}
		fake.current = renderWithConfig(cluster, m, bundle, layerCluster, nil, drifted)

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())

		cond := readyCondition(fetchMachine(m))
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNodeDrifted))
	})
})
