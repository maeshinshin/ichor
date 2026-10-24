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

package v1alpha1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newConfigPatch(layer string, target *TalosConfigPatchTarget) *TalosConfigPatch {
	return &TalosConfigPatch{
		GenerateName: "patch-",
		Namespace:    "default",
		Spec: TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "cluster"},
			Layer:      layer,
			Target:     target,
		},
	}
}

var _ = Describe("TalosConfigPatch", func() {
	DescribeTable("rejected target/layer combinations",
		func(layer string, target *TalosConfigPatchTarget) {
			Expect(k8sClient.Create(context.Background(), newConfigPatch(layer, target))).NotTo(Succeed())
		},
		Entry("two of three target fields set", "role",
			&TalosConfigPatchTarget{Role: "worker", MachineRef: &corev1.LocalObjectReference{Name: "m"}}),
		Entry("target set but none of its fields chosen", "role", &TalosConfigPatchTarget{}),
		Entry("layer cluster with a target", "cluster", &TalosConfigPatchTarget{Role: "controlplane"}),
		Entry("layer machine without a target", "machine", nil),
	)

	DescribeTable("accepted target/layer combinations",
		func(layer string, target *TalosConfigPatchTarget) {
			Expect(k8sClient.Create(context.Background(), newConfigPatch(layer, target))).To(Succeed())
		},
		Entry("layer cluster without a target", "cluster", nil),
		Entry("layer role targeting a role", "role", &TalosConfigPatchTarget{Role: "worker"}),
		Entry("layer selector targeting labels", "selector",
			&TalosConfigPatchTarget{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}}),
		Entry("layer machine targeting a machine", "machine",
			&TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: "m"}}),
	)
})

func newCluster(secretsRef corev1.LocalObjectReference, count int32) *TalosCluster {
	return &TalosCluster{
		GenerateName: "cluster-",
		Namespace:    "default",
		Spec: TalosClusterSpec{
			Endpoint:          TalosClusterEndpoint{Type: "vip", Address: "10.0.0.1"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: "v1.36.0",
			VersionContract:   "v1.14",
			SecretsRef:        secretsRef,
			ControlPlane:      TalosClusterControlPlane{Count: count},
		},
	}
}

var _ = Describe("TalosCluster", func() {
	It("rejects an empty secretsRef", func() {
		Expect(k8sClient.Create(context.Background(), newCluster(corev1.LocalObjectReference{}, 3))).NotTo(Succeed())
	})

	DescribeTable("controlPlane.count",
		func(count int32, accepted bool) {
			err := k8sClient.Create(context.Background(), newCluster(corev1.LocalObjectReference{Name: "secrets"}, count))
			if accepted {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("1 is accepted", int32(1), true),
		Entry("3 is accepted", int32(3), true),
		Entry("5 is accepted", int32(5), true),
		Entry("2 is rejected", int32(2), false),
		Entry("4 is rejected", int32(4), false),
	)
})
