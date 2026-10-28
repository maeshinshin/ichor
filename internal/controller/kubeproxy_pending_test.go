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
	"github.com/maeshinshin/ichor/pkg/render"
)

// A Git-tracked cluster commits spec.cni.kubeProxy: replaced before the switch runs (ADR 0066
// amendment): the node still has kube-proxy, and the render already says disabled.
var _ = Describe("a kube-proxy mode committed to spec ahead of the switch", func() {
	type fixture struct {
		cluster *ichorv1alpha1.TalosCluster
		m       *ichorv1alpha1.TalosMachine
		current []byte
		fake    *fakeTalosClient
	}

	setup := func() fixture {
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
				CNI:               ichorv1alpha1.TalosClusterCNI{Preset: "cilium", KubeProxy: modeEnabled},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)
		m.Spec.Role = roleControlPlane
		Expect(k8sClient.Update(ctx, m)).To(Succeed())
		setHostname(m, m.Name)
		newTestNode(m.Name, true)

		patch := newTestPatch(cluster.Name, layerCluster, nil, skeletonFor(roleControlPlane))

		// The node runs what the cluster renders while kube-proxy is enabled.
		running := cluster.DeepCopy()
		running.Spec.CNI.KubeProxy = modeEnabled

		current, _, err := render.Render(running, fetchMachine(m), bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		// The Git-first edit: spec and the consent annotation, no status.
		cluster.Spec.CNI.KubeProxy = modeReplaced
		cluster.Annotations = map[string]string{ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation: modeReplaced}

		return fixture{cluster: cluster, m: m, current: current, fake: &fakeTalosClient{current: current, bootID: testBootIDBefore}}
	}

	It("reports KubeProxySwitchPending, never RebootRequired, and points at the switch", func() {
		f := setup()
		Expect(k8sClient.Update(ctx, f.cluster)).To(Succeed())

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(f.fake)}
		_, err := reconcileMachine(r, f.m) // adds the removal finalizer of an approved control plane
		Expect(err).NotTo(HaveOccurred())

		_, err = reconcileMachine(r, f.m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(f.m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reasonKubeProxySwitchPending))
		Expect(cond.Message).To(ContainSubstring("ichor cni kube-proxy replaced"))
		Expect(cond.Message).NotTo(ContainSubstring("config apply"))
		Expect(got.Status.RebootPaths).To(BeEmpty(), "a config rollout must not pick the machine up")
		Expect(f.fake.applyCalls).To(BeEmpty())
		Expect(f.fake.rebootCalls).To(BeZero())
	})

	It("is not rebooted by a config rollout", func() {
		f := setup()
		Expect(k8sClient.Update(ctx, f.cluster)).To(Succeed())

		_, err := newRolloutAdvancer(f.fake).Advance(ctx, f.cluster, fetchMachine(f.m), nil)
		Expect(err).To(MatchError(ContainSubstring("ichor cni kube-proxy replaced")))
		Expect(f.fake.stagedCalls).To(BeEmpty())
		Expect(f.fake.applyCalls).To(BeEmpty())
		Expect(f.fake.rebootCalls).To(BeZero())
	})
})
