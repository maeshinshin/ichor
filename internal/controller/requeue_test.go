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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// A False Ready that time can fix must come back by itself; one that waits for an operator must not.
var _ = Describe("TalosMachineReconciler requeue after a failure", func() {
	setup := func() (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")

		return cluster, newTestMachine(cluster.Name, true), bundle
	}

	It("retries on its own once the node answers again, without another event", func() {
		cluster, m, bundle := setup()
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
		_, m, _ := setup()
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

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(dialed).To(Equal([]string{storageTestEndpoint, "10.9.9.9:50000"}), "the next round dials the observed address")

		// An operator editing spec.endpoint overrides what was observed.
		got = fetchMachine(m)
		got.Spec.Endpoint = "10.0.0.77:50000"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(got.DialEndpoint()).To(Equal("10.0.0.77:50000"))
	})

	It("asks to be looked at again when the node's current configuration cannot be read", func() {
		_, m, _ := setup()
		fake := &fakeTalosClient{currentErr: errors.New("read failed")}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonCurrentConfigUnavailable))
	})

	It("asks to be looked at again when the secrets bundle is missing", func() {
		cluster := newTestCluster("no-such-secret", "v1.14")
		m := newTestMachine(cluster.Name, true)
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(transientRecheck))
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonSecretsBundleUnavailable))
	})

	It("retries a failed apply, and applies once the node accepts it", func() {
		cluster, m, bundle := setup()
		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())
		newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())

		fake := &fakeTalosClient{current: current, applyErr: errors.New("node busy")}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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
		cluster, m, bundle := setup()
		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())
		disabled := true
		desired := minimalConfigSkeleton()
		desired.ClusterConfig.ProxyConfig = &typedconfig.ProxyConfig{Disabled: &disabled}
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonRebootRequired))
	})

	It("does not poll for an install-time-only change", func() {
		cluster, m, bundle := setup()
		target := &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: m.Name}}
		current := renderWithConfig(cluster, m, bundle, "machine", target, minimalConfigSkeleton())
		desired := minimalConfigSkeleton()
		wipe := true
		desired.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/sdb", InstallWipe: &wipe}
		newTestPatch(cluster.Name, "machine", target, desired)

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		res, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonInstallTimeOnlyChange))
	})

	It("does not poll for drift under driftPolicy warnOnly", func() {
		cluster, m, bundle := setup()
		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		rendered, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: rendered}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}
		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

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
