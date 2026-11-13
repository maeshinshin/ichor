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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/render"
)

// A Git-tracked cluster commits spec.cni.kubeProxy and the consent annotation before the switch
// runs: the node still runs the old mode, and the render already says the new
// one. Both directions are held back for the switch.
var _ = Describe("a kube-proxy mode committed to spec ahead of the switch", func() {
	type fixture struct {
		cluster *ichorv1alpha1.TalosCluster
		m       *ichorv1alpha1.TalosMachine
		fake    *fakeTalosClient
	}

	// setup creates a cluster in mode from, a node running it, then edits the spec to mode to with
	// the consent annotation and no status. The edit is not yet sent: the caller updates it.
	setup := func(from, to string) fixture {
		bundle := newTestBundle(testK8sContract)
		secret := newTestSecret(bundle)
		// Only cilium may replace kube-proxy, and the preset cannot change later.
		cluster := &ichorv1alpha1.TalosCluster{
			GenerateName: testClusterGenerate, Namespace: tmNamespace,
			Spec: ichorv1alpha1.TalosClusterSpec{
				Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
				TalosVersion:      testTalosVersion,
				KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
				VersionContract:   testK8sContract,
				SecretsRef:        corev1.LocalObjectReference{Name: secret.Name},
				ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
				CNI:               ichorv1alpha1.TalosClusterCNI{Preset: "cilium", KubeProxy: from},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)
		m.Spec.Role = roleControlPlane
		Expect(k8sClient.Update(ctx, m)).To(Succeed())
		setHostname(m, m.Name)
		newTestNode(m.Name, true)

		patch := newTestPatch(cluster.Name, layerCluster, nil, skeletonFor(roleControlPlane))

		// The node runs what the cluster renders in the old mode.
		current, _, err := render.Render(cluster, fetchMachine(m), bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		// The Git-first edit: spec and the consent annotation, no status.
		cluster.Spec.CNI.KubeProxy = to
		cluster.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: to}

		return fixture{cluster: cluster, m: m, fake: &fakeTalosClient{current: current, bootID: testBootIDBefore}}
	}

	for _, d := range []struct{ from, to string }{{modeEnabled, modeReplaced}, {modeReplaced, modeEnabled}} {
		Context("from "+d.from+" to "+d.to, func() {
			It("reports KubeProxySwitchPending, never RebootRequired, and points at the switch", func() {
				f := setup(d.from, d.to)
				Expect(k8sClient.Update(ctx, f.cluster)).To(Succeed())

				r := newTestReconciler(f.fake)
				_, err := reconcileMachine(r, f.m) // adds the removal finalizer of an approved control plane
				Expect(err).NotTo(HaveOccurred())

				reconcileOK(r, f.m)

				got := fetchMachine(f.m)
				cond := readyCondition(got)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(reasonKubeProxySwitchPending))
				Expect(cond.Message).To(ContainSubstring("ichor set kube-proxy " + d.to))
				Expect(cond.Message).NotTo(ContainSubstring("config apply"))
				Expect(got.Status.RebootPaths).To(BeEmpty(), "a config rollout must not pick the machine up")
				Expect(f.fake.applyCalls).To(BeEmpty())
				Expect(f.fake.rebootCalls).To(BeZero())
			})

			It("is not rebooted by a config rollout", func() {
				f := setup(d.from, d.to)
				Expect(k8sClient.Update(ctx, f.cluster)).To(Succeed())

				_, err := newRolloutAdvancer(f.fake).Advance(ctx, f.cluster, fetchMachine(f.m), nil)
				Expect(err).To(MatchError(ContainSubstring("ichor set kube-proxy " + d.to)))
				Expect(f.fake.stagedCalls).To(BeEmpty())
				Expect(f.fake.applyCalls).To(BeEmpty())
				Expect(f.fake.rebootCalls).To(BeZero())
			})
		})
	}
})

// The annotation is what makes a kube-proxy difference a pending switch; without it (a patch-made
// cluster.proxy.disabled, a stale annotation, a finished switch) it stays an ordinary reboot-required
// change. The patch-made case end to end is "applies nothing and never takes the cluster lock".
var _ = Describe("kubeProxySwitchOnly", func() {
	impact := fieldmeta.Impact{RebootRequired: true, RebootPaths: []string{"cluster.proxy.disabled"}}
	cp := &ichorv1alpha1.TalosMachine{Spec: ichorv1alpha1.TalosMachineSpec{Role: roleControlPlane}}

	cluster := func(mode, annotation, status string) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{CNI: ichorv1alpha1.TalosClusterCNI{KubeProxy: mode}}}
		if annotation != "" {
			c.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: annotation}
		}

		if status != "" {
			c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: status}
		}

		return c
	}

	DescribeTable("holds a kube-proxy-only change back only for a consented, unfinished switch",
		func(c *ichorv1alpha1.TalosCluster, want bool) {
			Expect(kubeProxySwitchOnly(c, cp, impact)).To(Equal(want))
		},
		Entry("no annotation, so RebootRequired", cluster(modeEnabled, "", ""), false),
		Entry("an annotation for another mode", cluster(modeEnabled, modeReplaced, ""), false),
		Entry("replaced, consented, no status", cluster(modeReplaced, modeReplaced, ""), true),
		Entry("enabled, consented, no status", cluster(modeEnabled, modeEnabled, ""), true),
		Entry("consented, status still the old mode", cluster(modeEnabled, modeEnabled, modeReplaced), true),
		Entry("a finished switch with a leftover annotation", cluster(modeReplaced, modeReplaced, modeReplaced), false),
	)

	It("leaves a kube-proxy change that comes with another reboot path to RebootRequired", func() {
		mixed := fieldmeta.Impact{RebootRequired: true, RebootPaths: []string{"cluster.proxy.disabled", "machine.network"}}
		Expect(kubeProxySwitchOnly(cluster(modeReplaced, modeReplaced, ""), cp, mixed)).To(BeFalse())
	})
})
