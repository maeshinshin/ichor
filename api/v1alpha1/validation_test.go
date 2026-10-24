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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Shared literals, hoisted so they aren't repeated across the table entries below (goconst).
const (
	testNamespace       = "default"
	literalCluster      = "cluster"
	literalControlPlane = "controlplane"

	kindSysctlConfig       = "SysctlConfig"
	kindKernelModuleConfig = "KernelModuleConfig"
	kindConfig             = "Config"
)

func newConfigPatch(layer string, target *TalosConfigPatchTarget) *TalosConfigPatch {
	return &TalosConfigPatch{
		GenerateName: "patch-",
		Namespace:    testNamespace,
		Spec: TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
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
		Entry("layer cluster with a target", literalCluster, &TalosConfigPatchTarget{Role: literalControlPlane}),
		Entry("layer machine without a target", "machine", nil),
	)

	DescribeTable("accepted target/layer combinations",
		func(layer string, target *TalosConfigPatchTarget) {
			Expect(k8sClient.Create(context.Background(), newConfigPatch(layer, target))).To(Succeed())
		},
		Entry("layer cluster without a target", literalCluster, nil),
		Entry("layer role targeting a role", "role", &TalosConfigPatchTarget{Role: "worker"}),
		Entry("layer selector targeting labels", "selector",
			&TalosConfigPatchTarget{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}}),
		Entry("layer machine targeting a machine", "machine",
			&TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: "m"}}),
	)
})

// minimalConfig is the smallest typedconfig.Config that satisfies the required fields down the
// one branch (machine, cluster) the discrimination tests touch.
func minimalConfig() *typedconfig.Config {
	return &typedconfig.Config{
		ConfigVersion: "v1alpha1",
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     literalControlPlane,
			MachineToken:    "328hom.uqjzh6jnn2eie9oi",
			MachineCertSANs: []string{"10.0.0.10"},
		},
		ClusterConfig: &typedconfig.ClusterConfig{},
	}
}

func minimalSysctlConfig() *typedconfig.SysctlConfigV1Alpha1 {
	return &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.ipv4.ip_forward": "1"}}
}

func minimalKernelModuleConfig() *typedconfig.KernelModuleConfigV1Alpha1 {
	return &typedconfig.KernelModuleConfigV1Alpha1{MetaName: "btrfs"}
}

func newConfigPatchWithDocument(doc TalosConfigPatchDocument) *TalosConfigPatch {
	return &TalosConfigPatch{
		GenerateName: "patch-",
		Namespace:    testNamespace,
		Spec: TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
			Layer:      literalCluster,
			Documents:  []TalosConfigPatchDocument{doc},
		},
	}
}

var _ = Describe("TalosConfigPatchDocument", func() {
	DescribeTable("accepted documents",
		func(doc TalosConfigPatchDocument) {
			Expect(k8sClient.Create(context.Background(), newConfigPatchWithDocument(doc))).To(Succeed())
		},
		Entry("SysctlConfig with sysctlConfig populated", TalosConfigPatchDocument{
			Kind:         kindSysctlConfig,
			SysctlConfig: minimalSysctlConfig(),
		}),
		Entry("KernelModuleConfig with kernelModuleConfig populated", TalosConfigPatchDocument{
			Kind:               kindKernelModuleConfig,
			KernelModuleConfig: minimalKernelModuleConfig(),
		}),
		Entry("Config with config populated", TalosConfigPatchDocument{
			Kind:   kindConfig,
			Config: minimalConfig(),
		}),
	)

	DescribeTable("rejected documents",
		func(doc TalosConfigPatchDocument) {
			Expect(k8sClient.Create(context.Background(), newConfigPatchWithDocument(doc))).NotTo(Succeed())
		},
		Entry("kind SysctlConfig with config populated instead", TalosConfigPatchDocument{
			Kind:   kindSysctlConfig,
			Config: minimalConfig(),
		}),
		Entry("kind KernelModuleConfig with sysctlConfig populated instead", TalosConfigPatchDocument{
			Kind:         kindKernelModuleConfig,
			SysctlConfig: minimalSysctlConfig(),
		}),
		Entry("kind Config with kernelModuleConfig populated instead", TalosConfigPatchDocument{
			Kind:               kindConfig,
			KernelModuleConfig: minimalKernelModuleConfig(),
		}),
		Entry("two fields populated", TalosConfigPatchDocument{
			Kind:               kindSysctlConfig,
			SysctlConfig:       minimalSysctlConfig(),
			KernelModuleConfig: minimalKernelModuleConfig(),
		}),
		Entry("no field populated", TalosConfigPatchDocument{
			Kind: kindSysctlConfig,
		}),
		Entry("kind outside the allow-list", TalosConfigPatchDocument{
			Kind:         "NetworkRuleConfig",
			SysctlConfig: minimalSysctlConfig(),
		}),
	)

	It("round-trips a non-trivial Config document through the API server", func() {
		want := &typedconfig.Config{
			ConfigVersion: "v1alpha1",
			MachineConfig: &typedconfig.MachineConfig{
				MachineType:     literalControlPlane,
				MachineToken:    "328hom.uqjzh6jnn2eie9oi",
				MachineCertSANs: []string{"10.0.0.10", "203.0.113.10"},
				// MachineInstall and MachineKubelet are deprecated in favor of multi-document
				// config kinds outside the phase 2 allow-list; they are still how a
				// disk selector and extraArgs reach v1alpha1.Config today.
				MachineInstall: &typedconfig.InstallConfig{
					InstallDiskSelector: &typedconfig.InstallDiskSelector{
						Model: "QEMU HARDDISK",
						Type:  "ssd",
					},
					InstallImage: "factory.talos.dev/metal-installer/abc123:latest",
					InstallWipe:  ptr.To(true),
				},
				MachineKubelet: &typedconfig.KubeletConfig{
					KubeletExtraArgs: map[string][]string{
						"node-labels": {"foo=bar", "baz=qux"},
					},
				},
			},
			ClusterConfig: &typedconfig.ClusterConfig{
				ClusterName: "test-cluster",
				ControlPlane: &typedconfig.ControlPlaneConfig{
					Endpoint:           ptr.To("https://10.0.0.1:6443"),
					LocalAPIServerPort: 6443,
				},
			},
		}

		created := newConfigPatchWithDocument(TalosConfigPatchDocument{Kind: kindConfig, Config: want})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		readBack := &TalosConfigPatch{}
		key := types.NamespacedName{Name: created.Name, Namespace: created.Namespace}
		Expect(k8sClient.Get(context.Background(), key, readBack)).To(Succeed())

		Expect(readBack.Spec.Documents).To(HaveLen(1))
		Expect(readBack.Spec.Documents[0].Kind).To(Equal(kindConfig))

		wantMachinery, err := want.ToMachinery()
		Expect(err).NotTo(HaveOccurred())

		gotMachinery, err := readBack.Spec.Documents[0].Config.ToMachinery()
		Expect(err).NotTo(HaveOccurred())

		Expect(gotMachinery).To(Equal(wantMachinery))
	})
})

func newCluster(secretsRef corev1.LocalObjectReference, count int32) *TalosCluster {
	return &TalosCluster{
		GenerateName: "cluster-",
		Namespace:    testNamespace,
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
