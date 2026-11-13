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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Shared literals, hoisted so they aren't repeated across the table entries below (goconst).
const (
	testNamespace        = "default"
	literalCluster       = "cluster"
	literalControlPlane  = "controlplane"
	literalWorker        = "worker"
	literalSecrets       = "secrets"
	literalConfigVersion = "v1alpha1"
	literalGenerateName  = "patch-"
	literalNodeIP        = "10.0.0.10"
	literalKindKey       = "kind"

	kindSysctlConfig       = "SysctlConfig"
	kindKernelModuleConfig = "KernelModuleConfig"
	kindConfig             = "Config"

	cniPresetFlannel   = "flannel"
	cniPresetCilium    = "cilium"
	tunnelVXLAN        = "vxlan"
	tunnelGeneve       = "geneve"
	presetCustom       = "custom"
	presetUnmanaged    = "unmanaged"
	kubeProxyEnabled   = "enabled"
	kubeProxyReplaced  = "replaced"
	testCIDR           = "192.0.2.0/24"
	testTrue           = "true"
	testPodCIDR        = "10.200.0.0/16"
	metricsAllowedCIDR = "10.20.0.0/16"

	testRegistryHost   = "registry.example.com"
	testMirrorEndpoint = "https://mirror.example.com"
)

func newConfigPatch(layer string, target *TalosConfigPatchTarget) *TalosConfigPatch {
	return &TalosConfigPatch{
		GenerateName: literalGenerateName,
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
			&TalosConfigPatchTarget{Role: literalWorker, MachineRef: &corev1.LocalObjectReference{Name: "m"}}),
		Entry("target set but none of its fields chosen", "role", &TalosConfigPatchTarget{}),
		Entry("layer cluster with a target", literalCluster, &TalosConfigPatchTarget{Role: literalControlPlane}),
		Entry("layer machine without a target", "machine", nil),
		Entry("layer role with a machineRef target", "role",
			&TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: "m"}}),
		Entry("layer role with a labelSelector target", "role",
			&TalosConfigPatchTarget{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}}),
		Entry("layer selector with a role target", "selector", &TalosConfigPatchTarget{Role: literalWorker}),
		Entry("layer machine with a role target", "machine", &TalosConfigPatchTarget{Role: literalWorker}),
		Entry("layer machine with a labelSelector target", "machine",
			&TalosConfigPatchTarget{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k": "v"}}}),
	)

	DescribeTable("accepted target/layer combinations",
		func(layer string, target *TalosConfigPatchTarget) {
			Expect(k8sClient.Create(context.Background(), newConfigPatch(layer, target))).To(Succeed())
		},
		Entry("layer cluster without a target", literalCluster, nil),
		Entry("layer role targeting a role", "role", &TalosConfigPatchTarget{Role: literalWorker}),
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
		ConfigVersion: literalConfigVersion,
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     literalControlPlane,
			MachineCertSANs: []string{literalNodeIP},
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
		GenerateName: literalGenerateName,
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

	// The schema bounds a document to kind plus one field (minProperties and maxProperties 2); that the
	// field is the one kind names is Validate's, which the webhook and the renderer call.
	DescribeTable("a document the schema admits but Validate refuses, naming the kind and the fields",
		func(doc TalosConfigPatchDocument, kind, populated string) {
			Expect(k8sClient.Create(context.Background(), newConfigPatchWithDocument(doc))).To(Succeed())
			Expect(doc.Validate()).To(MatchError(And(ContainSubstring(kind), ContainSubstring(populated))))
		},
		Entry("kind SysctlConfig with config populated instead", TalosConfigPatchDocument{
			Kind:   kindSysctlConfig,
			Config: minimalConfig(),
		}, kindSysctlConfig, "config"),
		Entry("kind KernelModuleConfig with sysctlConfig populated instead", TalosConfigPatchDocument{
			Kind:         kindKernelModuleConfig,
			SysctlConfig: minimalSysctlConfig(),
		}, kindKernelModuleConfig, "sysctlConfig"),
		Entry("kind Config with kernelModuleConfig populated instead", TalosConfigPatchDocument{
			Kind:               kindConfig,
			KernelModuleConfig: minimalKernelModuleConfig(),
		}, kindConfig, "kernelModuleConfig"),
	)

	It("round-trips a non-trivial Config document through the API server", func() {
		want := &typedconfig.Config{
			ConfigVersion: literalConfigVersion,
			MachineConfig: &typedconfig.MachineConfig{
				MachineType:     literalControlPlane,
				MachineCertSANs: []string{literalNodeIP, "203.0.113.10"},
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

	// PENDING: mirrorgen's secretFields table (internal/tools/mirrorgen/main.go)
	// correctly removes all nine secret-carrying fields from the CRD schema --
	// verified below by reading the object back and confirming none of the
	// nine survived. What is NOT yet confirmed is the acceptance criterion as
	// literally stated ("the API server rejects it, not structural-schema
	// pruning"): measured against this envtest apiserver, a plain
	// client.Create with no fieldValidation option set PRUNES unknown fields
	// (logs "unknown field ..." and a nil error) rather than rejecting the
	// request. That is a kubectl/client-side opt-in
	// (metav1.CreateOptions.FieldValidation=Strict), not something a CRD
	// schema can force on every caller by itself; the standard way to force
	// rejection server-side is a validating admission webhook, which is
	// outside this change's scope (internal/webhook/ is excluded). This is
	// flagged back to the coordinator rather than silently asserted either
	// way -- see the task report.
	It("drops all nine secret fields from a Config document, however it arrives", func() {
		caPair := map[string]any{"crt": "Y2EtY2VydA==", "key": "Y2Eta2V5"}

		u := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion":   GroupVersion.String(),
				literalKindKey: "TalosConfigPatch",
				"metadata": map[string]any{
					"generateName": literalGenerateName,
					"namespace":    testNamespace,
				},
				"spec": map[string]any{
					"clusterRef": map[string]any{"name": literalCluster},
					"layer":      literalCluster,
					"documents": []any{
						map[string]any{
							literalKindKey: kindConfig,
							"config": map[string]any{
								"version": literalConfigVersion,
								"machine": map[string]any{
									"type":     literalControlPlane,
									"certSANs": []any{literalNodeIP},
									// The two MachineConfig secret fields.
									"token": "328hom.uqjzh6jnn2eie9oi",
									"ca":    caPair,
								},
								"cluster": map[string]any{
									// The six ClusterConfig secret fields, plus
									// EtcdConfig.RootCA nested under "etcd".
									"secret":                 "cluster-secret",
									"token":                  "wlzjyw.bei2zfylhs2by0wd",
									"aescbcEncryptionSecret": "aescbc-secret",
									"ca":                     caPair,
									"aggregatorCA":           caPair,
									"serviceAccount":         map[string]any{"key": "c2Eta2V5"},
									"etcd":                   map[string]any{"ca": caPair},
								},
							},
						},
					},
				},
			},
		}

		Expect(k8sClient.Create(context.Background(), u)).To(Succeed())

		readBack := &unstructured.Unstructured{}
		readBack.SetGroupVersionKind(u.GroupVersionKind())
		key := types.NamespacedName{Name: u.GetName(), Namespace: u.GetNamespace()}
		Expect(k8sClient.Get(context.Background(), key, readBack)).To(Succeed())

		documents, _, err := unstructured.NestedSlice(readBack.Object, "spec", "documents")
		Expect(err).NotTo(HaveOccurred())
		Expect(documents).To(HaveLen(1))

		doc, ok := documents[0].(map[string]any) //nolint:forcetypeassert
		Expect(ok).To(BeTrue())

		machine, _, err := unstructured.NestedMap(doc, "config", "machine")
		Expect(err).NotTo(HaveOccurred())
		Expect(machine).NotTo(HaveKey("token"))
		Expect(machine).NotTo(HaveKey("ca"))

		cluster, _, err := unstructured.NestedMap(doc, "config", "cluster")
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster).NotTo(HaveKey("secret"))
		Expect(cluster).NotTo(HaveKey("token"))
		Expect(cluster).NotTo(HaveKey("aescbcEncryptionSecret"))
		Expect(cluster).NotTo(HaveKey("ca"))
		Expect(cluster).NotTo(HaveKey("aggregatorCA"))
		Expect(cluster).NotTo(HaveKey("serviceAccount"))
		if etcd, found, _ := unstructured.NestedMap(cluster, "etcd"); found { //nolint:errcheck
			Expect(etcd).NotTo(HaveKey("ca"))
		}
	})
})

func newCluster(secretsRef corev1.LocalObjectReference, count int32, schedulable string) *TalosCluster {
	return &TalosCluster{
		GenerateName: "cluster-",
		Namespace:    testNamespace,
		Spec: TalosClusterSpec{
			Endpoint:          TalosClusterEndpoint{Type: "vip", Address: "10.0.0.1"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: "v1.36.0",
			VersionContract:   "v1.14",
			SecretsRef:        secretsRef,
			ControlPlane:      TalosClusterControlPlane{Count: count, Schedulable: schedulable},
		},
	}
}

var _ = Describe("TalosCluster", func() {
	It("rejects an empty secretsRef", func() {
		Expect(k8sClient.Create(context.Background(), newCluster(corev1.LocalObjectReference{}, 3, "true"))).NotTo(Succeed())
	})

	DescribeTable("controlPlane.count",
		func(count int32, accepted bool) {
			expectAdmitted(k8sClient.Create(context.Background(), newCluster(corev1.LocalObjectReference{Name: literalSecrets}, count, "true")), accepted)
		},
		Entry("1 is accepted", int32(1), true),
		Entry("3 is accepted", int32(3), true),
		Entry("5 is accepted", int32(5), true),
		Entry("2 is rejected", int32(2), false),
		Entry("4 is rejected", int32(4), false),
	)

	// The endpoint type has no dns: a hostname is a loadBalancer address, so the enum is the three values.
	DescribeTable("endpoint.type",
		func(typ string, accepted bool) {
			c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
			c.Spec.Endpoint.Type = typ
			expectAdmitted(k8sClient.Create(context.Background(), c), accepted)
		},
		Entry("vip is accepted", "vip", true),
		Entry("loadBalancer is accepted", "loadBalancer", true),
		Entry("node is accepted", "node", true),
		Entry("dns is refused", "dns", false),
	)

	// Schedulable has no "auto" -- nothing on the object can resolve
	// it before any machine exists, so the field is required and limited to
	// true/false, and any other value (including empty) is refused.
	DescribeTable("controlPlane.schedulable",
		func(schedulable string, accepted bool) {
			expectAdmitted(k8sClient.Create(context.Background(), newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, schedulable)), accepted)
		},
		Entry("true is accepted", "true", true),
		Entry("false is accepted", "false", true),
		Entry("auto is rejected", "auto", false),
		Entry("empty string is rejected", "", false),
	)

	It("rejects a TalosCluster with controlPlane.schedulable omitted entirely", func() {
		u := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion":   GroupVersion.String(),
				literalKindKey: "TalosCluster",
				"metadata": map[string]any{
					"generateName": "cluster-",
					"namespace":    testNamespace,
				},
				"spec": map[string]any{
					"endpoint":          map[string]any{"type": "vip", "address": "10.0.0.1"},
					"talosVersion":      "v1.14.3",
					"kubernetesVersion": "v1.36.0",
					"versionContract":   "v1.14",
					"secretsRef":        map[string]any{"name": literalSecrets},
					"controlPlane":      map[string]any{"count": int64(3)},
				},
			},
		}

		Expect(k8sClient.Create(context.Background(), u)).NotTo(Succeed())
	})

	It("accepts a TalosCluster with no discovery declared at all (the default)", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
		Expect(c.Spec.Discovery).To(BeNil())
	})

	DescribeTable("discovery.addresses",
		func(addresses []string, accepted bool) {
			c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
			c.Spec.Discovery = &TalosClusterDiscovery{Addresses: addresses}

			expectAdmitted(k8sClient.Create(context.Background(), c), accepted)
		},
		Entry("a bare IPv4 address is accepted", []string{"192.0.2.10"}, true),
		Entry("several IPv4 addresses are accepted", []string{"192.0.2.10", "192.0.2.11"}, true),
		Entry("a hostname is rejected", []string{"node-a.example"}, false),
		Entry("an address with a port is rejected", []string{"192.0.2.10:50000"}, false),
		Entry("a CIDR is rejected here", []string{testCIDR}, false),
		Entry("an IPv6 address is rejected", []string{"::1"}, false),
	)

	DescribeTable("discovery.cidrs",
		func(cidrs []string, accepted bool) {
			c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
			c.Spec.Discovery = &TalosClusterDiscovery{CIDRs: cidrs}

			expectAdmitted(k8sClient.Create(context.Background(), c), accepted)
		},
		Entry("a /24 is accepted", []string{testCIDR}, true),
		Entry("a /32 is accepted", []string{"192.0.2.10/32"}, true),
		Entry("a /23 is rejected (larger than /24)", []string{"192.0.2.0/23"}, false),
		Entry("a /16 is rejected", []string{"192.0.0.0/16"}, false),
		Entry("a /8 is rejected", []string{"10.0.0.0/8"}, false),
		Entry("a bare address, not a CIDR, is rejected", []string{"192.0.2.0"}, false),
		Entry("an IPv6 CIDR is rejected", []string{"::1/128"}, false),
		Entry("a 5th CIDR is rejected (MaxItems=4)",
			[]string{testCIDR, "192.0.3.0/24", "192.0.4.0/24", "192.0.5.0/24", "192.0.6.0/24"}, false),
	)

	// Spec.controlPlaneMetrics is off by default and never exposes without a source CIDR.
	It("accepts a TalosCluster with no controlPlaneMetrics declared at all (the default)", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
		Expect(c.Spec.ControlPlaneMetrics).To(BeNil())
	})

	DescribeTable("controlPlaneMetrics",
		func(enabled bool, cidrs []string, accepted bool) {
			c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
			c.Spec.ControlPlaneMetrics = &TalosClusterControlPlaneMetrics{Enabled: enabled, AllowedCIDRs: cidrs}

			expectAdmitted(k8sClient.Create(context.Background(), c), accepted)
		},
		Entry("enabled with an IPv4 CIDR is accepted", true, []string{metricsAllowedCIDR}, true),
		Entry("enabled with an IPv6 CIDR is accepted", true, []string{"2001:db8::/32"}, true),
		Entry("a single host /32 is accepted", true, []string{"10.20.0.7/32"}, true),
		Entry("disabled with no CIDR is accepted", false, nil, true),
		Entry("disabled with a CIDR is accepted (kept for later)", false, []string{metricsAllowedCIDR}, true),
		Entry("enabled with no CIDR is rejected", true, nil, false),
		Entry("enabled with an empty list is rejected", true, []string{}, false),
		Entry("0.0.0.0/0 is rejected (no restriction)", true, []string{"0.0.0.0/0"}, false),
		Entry("::/0 is rejected", true, []string{"::/0"}, false),
		Entry("a bare address is rejected", true, []string{"10.20.0.1"}, false),
		Entry("garbage is rejected", true, []string{"not-a-cidr"}, false),
		Entry("a bad entry beside a good one is rejected", true, []string{metricsAllowedCIDR, "nope"}, false),
		Entry("a 9th CIDR is rejected (MaxItems=8)", true, []string{
			"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24",
			"10.0.5.0/24", "10.0.6.0/24", "10.0.7.0/24", "10.0.8.0/24",
		}, false),
	)

	// Spec.registries is the reference form for a private registry's mirror and pull
	// credentials -- a plain typed field for everything that isn't secret, authSecretRef for
	// everything that is.
	It("accepts a TalosCluster with no registries declared at all (the default)", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
		Expect(c.Spec.Registries).To(BeNil())
	})

	It("accepts an anonymous mirror with no authSecretRef", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{
			{Host: testRegistryHost, Endpoints: []string{testMirrorEndpoint}},
		}
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
	})

	It("accepts a mirror with a username and an authSecretRef", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{
			{
				Host:          testRegistryHost,
				Endpoints:     []string{testMirrorEndpoint},
				Username:      "deploy",
				AuthSecretRef: &corev1.LocalObjectReference{Name: "registry-creds"},
			},
		}
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
	})

	It("accepts \"*\" as host, and a host with an explicit port", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{
			{Host: "*", Endpoints: []string{testMirrorEndpoint}},
			{Host: testRegistryHost + ":5000", Endpoints: []string{testMirrorEndpoint + "-port"}},
		}
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())
	})

	It("rejects a host that is neither \"*\" nor a hostname[:port]", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{{Host: "not a host/with slash", Endpoints: []string{testMirrorEndpoint}}}
		Expect(k8sClient.Create(context.Background(), c)).NotTo(Succeed())
	})

	It("rejects an endpoint that is not a URL", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{{Host: testRegistryHost, Endpoints: []string{"not a url"}}}
		Expect(k8sClient.Create(context.Background(), c)).NotTo(Succeed())
	})

	It("rejects a registries entry with no host", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{{Endpoints: []string{testMirrorEndpoint}}}
		Expect(k8sClient.Create(context.Background(), c)).NotTo(Succeed())
	})

	It("rejects a registries entry with no endpoints", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{{Host: testRegistryHost}}
		Expect(k8sClient.Create(context.Background(), c)).NotTo(Succeed())
	})

	It("rejects two registries entries with the same host", func() {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Registries = []TalosClusterRegistry{
			{Host: testRegistryHost, Endpoints: []string{testMirrorEndpoint + "-a"}},
			{Host: testRegistryHost, Endpoints: []string{testMirrorEndpoint + "-b"}},
		}
		Expect(k8sClient.Create(context.Background(), c)).NotTo(Succeed())
	})
})

// newClusterWithCNI is newCluster plus an explicit spec.cni, for the CNI preset/kubeProxy tests
// below (docs/design/architecture.md, "CNI").
func newClusterWithCNI(cniSpec TalosClusterCNI) *TalosCluster {
	c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
	c.Spec.CNI = cniSpec
	return c
}

func customCNISource() TalosClusterCNICustomSource {
	return TalosClusterCNICustomSource{
		RepoURL:        "https://example.com/charts",
		Chart:          "my-cni",
		TargetRevision: "1.0.0",
	}
}

var _ = Describe("TalosCluster.spec.cni", func() {
	It("defaults preset to cilium and kubeProxy to enabled when cni is omitted entirely", func() {
		created := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		readBack := &TalosCluster{}
		key := types.NamespacedName{Name: created.Name, Namespace: created.Namespace}
		Expect(k8sClient.Get(context.Background(), key, readBack)).To(Succeed())

		Expect(readBack.Spec.CNI.Preset).To(Equal(cniPresetCilium))
		Expect(readBack.Spec.CNI.KubeProxy).To(Equal(kubeProxyEnabled))
	})

	DescribeTable("preset/kubeProxy/custom combinations at creation",
		func(cniSpec TalosClusterCNI, accepted bool) {
			expectAdmitted(k8sClient.Create(context.Background(), newClusterWithCNI(cniSpec)), accepted)
		},
		Entry("flannel with kubeProxy enabled", TalosClusterCNI{Preset: cniPresetFlannel, KubeProxy: kubeProxyEnabled}, true),
		Entry("flannel with kubeProxy replaced is rejected: flannel has no kube-proxy-free mode",
			TalosClusterCNI{Preset: cniPresetFlannel, KubeProxy: kubeProxyReplaced}, false),
		Entry("cilium with kubeProxy replaced", TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyReplaced}, true),
		Entry("unmanaged with kubeProxy enabled", TalosClusterCNI{Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled}, true),
		Entry("unmanaged with kubeProxy replaced records the node's setting",
			TalosClusterCNI{Preset: presetUnmanaged, KubeProxy: kubeProxyReplaced}, true),
		Entry("unmanaged with talosCNI none", TalosClusterCNI{
			Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled, Unmanaged: &TalosClusterCNIUnmanaged{TalosCNI: "none"},
		}, true),
		Entry("unmanaged with an unknown talosCNI is rejected", TalosClusterCNI{
			Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled, Unmanaged: &TalosClusterCNIUnmanaged{TalosCNI: "flannel"},
		}, false),
		Entry("unmanaged with cilium values is rejected: cilium only applies to the cilium preset", TalosClusterCNI{
			Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled,
			Cilium: &TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(`{}`)}},
		}, false),
		Entry("unmanaged with a tunnel is rejected: tunnel only applies to cilium",
			TalosClusterCNI{Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled, Tunnel: tunnelVXLAN}, false),
		Entry("cilium with unmanaged populated is rejected: unmanaged only applies to the unmanaged preset", TalosClusterCNI{
			Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled, Unmanaged: &TalosClusterCNIUnmanaged{TalosCNI: "none"},
		}, false),
		Entry("custom without custom populated is rejected",
			TalosClusterCNI{Preset: presetCustom, KubeProxy: kubeProxyEnabled}, false),
		Entry("custom with custom populated", TalosClusterCNI{
			Preset: presetCustom, KubeProxy: kubeProxyEnabled,
			Custom: &TalosClusterCNICustom{Source: customCNISource()},
		}, true),
		Entry("flannel with custom populated is rejected: custom must be set only when preset is custom",
			TalosClusterCNI{
				Preset: cniPresetFlannel, KubeProxy: kubeProxyEnabled,
				Custom: &TalosClusterCNICustom{Source: customCNISource()},
			}, false),
	)

	// The preset cannot be changed after the cluster exists: swapping a CNI takes the network
	// down and is out of scope (docs/design/architecture.md, "CNI").
	It("rejects changing preset after creation", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Preset = cniPresetFlannel
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	It("accepts an update that leaves preset unchanged", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Labels = map[string]string{"touched": testTrue}
		Expect(k8sClient.Update(context.Background(), created)).To(Succeed())
	})

	// Switching kubeProxy is refused unless annotated, since the switch must run in the one safe
	// order -- new side up, verify, old side down -- which nothing at this layer does for the
	// caller automatically (docs/design/architecture.md, "CNI").
	It("rejects switching kubeProxy without the confirmation annotation", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.KubeProxy = kubeProxyReplaced
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	It("accepts switching kubeProxy with the confirmation annotation", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Annotations = map[string]string{TalosClusterConfirmKubeProxySwitchAnnotation: kubeProxyReplaced}
		created.Spec.CNI.KubeProxy = kubeProxyReplaced
		Expect(k8sClient.Update(context.Background(), created)).To(Succeed())
	})

	It("rejects switching kubeProxy when the annotation names a different mode", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyReplaced})
		created.Annotations = map[string]string{TalosClusterConfirmKubeProxySwitchAnnotation: kubeProxyReplaced}
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.KubeProxy = kubeProxyEnabled
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	It("rejects switching kubeProxy when the annotation is a bare marker", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Annotations = map[string]string{TalosClusterConfirmKubeProxySwitchAnnotation: "true"}
		created.Spec.CNI.KubeProxy = kubeProxyReplaced
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})
})

var _ = Describe("TalosCluster.spec.cni unmanaged preset", func() {
	It("defaults unmanaged.talosCNI to default", func() {
		created := newClusterWithCNI(TalosClusterCNI{
			Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled, Unmanaged: &TalosClusterCNIUnmanaged{},
		})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		readBack := &TalosCluster{}
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: created.Name, Namespace: created.Namespace}, readBack)).To(Succeed())
		Expect(readBack.Spec.CNI.Unmanaged.TalosCNI).To(Equal("default"))
	})

	It("rejects changing an unmanaged cluster to cilium", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Preset = cniPresetCilium
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	DescribeTable("status.cni.detected",
		func(detected string, accepted bool) {
			created := newClusterWithCNI(TalosClusterCNI{Preset: presetUnmanaged, KubeProxy: kubeProxyEnabled})
			Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

			created.Status.CNI = &TalosClusterCNIStatus{Detected: detected, DetectedVersion: "v1.0.0"}
			expectAdmitted(k8sClient.Status().Update(context.Background(), created), accepted)
		},
		Entry("cilium", "cilium", true),
		Entry("flannel", "flannel", true),
		Entry("calico", "calico", true),
		Entry("other", "other", true),
		Entry("none", "none", true),
		Entry("unknown value", "weave", false),
	)
})

// cni.tunnel is Cilium-only, immutable once set, and an existing cluster that never recorded one
// may only add vxlan (its running default). The CEL rules are on TalosClusterCNI.
var _ = Describe("TalosCluster.spec.cni.tunnel", func() {
	DescribeTable("tunnel at creation",
		func(cniSpec TalosClusterCNI, accepted bool) {
			expectAdmitted(k8sClient.Create(context.Background(), newClusterWithCNI(cniSpec)), accepted)
		},
		Entry("cilium with geneve", TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled, Tunnel: tunnelGeneve}, true),
		Entry("flannel with geneve is rejected: tunnel only applies to cilium",
			TalosClusterCNI{Preset: cniPresetFlannel, KubeProxy: kubeProxyEnabled, Tunnel: tunnelGeneve}, false),
		Entry("custom with tunnel is rejected: tunnel only applies to cilium",
			TalosClusterCNI{
				Preset: presetCustom, KubeProxy: kubeProxyEnabled, Tunnel: tunnelVXLAN,
				Custom: &TalosClusterCNICustom{Source: customCNISource()},
			}, false),
	)

	It("rejects changing tunnel after creation", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled, Tunnel: tunnelGeneve})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Tunnel = tunnelVXLAN
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	It("rejects removing tunnel after creation", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled, Tunnel: tunnelGeneve})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Tunnel = ""
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})

	It("accepts adding vxlan to an existing cilium cluster that never recorded a tunnel", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Tunnel = tunnelVXLAN
		Expect(k8sClient.Update(context.Background(), created)).To(Succeed())
	})

	It("rejects adding geneve to an existing cilium cluster that never recorded a tunnel", func() {
		created := newClusterWithCNI(TalosClusterCNI{Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		created.Spec.CNI.Tunnel = tunnelGeneve
		Expect(k8sClient.Update(context.Background(), created)).NotTo(Succeed())
	})
})

// Free-form values survive pruning and stack defaults apply.
var _ = Describe("free-form values", func() {
	It("keeps unknown keys under spec.cni.cilium.values", func() {
		created := newClusterWithCNI(TalosClusterCNI{
			Preset: cniPresetCilium, KubeProxy: kubeProxyEnabled,
			Cilium: &TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(`{"hubble":{"relay":{"enabled":true}},"anything":1}`)}},
		})
		Expect(k8sClient.Create(context.Background(), created)).To(Succeed())

		readBack := &TalosCluster{}
		key := types.NamespacedName{Name: created.Name, Namespace: created.Namespace}
		Expect(k8sClient.Get(context.Background(), key, readBack)).To(Succeed())
		Expect(readBack.Spec.CNI.Cilium.Values.Raw).To(MatchJSON(`{"hubble":{"relay":{"enabled":true}},"anything":1}`))
	})
})

// Spec.network, immutable once set; spec.hostname and spec.install on TalosMachine.
var _ = Describe("TalosCluster.spec.network", func() {
	newNet := func(pods, svcs []string) *TalosCluster {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Network = &TalosClusterNetwork{PodSubnets: pods, ServiceSubnets: svcs}

		return c
	}

	DescribeTable("subnet lists at creation",
		func(pods, svcs []string, accepted bool) {
			expectAdmitted(k8sClient.Create(context.Background(), newNet(pods, svcs)), accepted)
		},
		Entry("both ranges", []string{testPodCIDR}, []string{"10.201.0.0/16"}, true),
		Entry("only pods", []string{testPodCIDR}, nil, true),
		Entry("dual stack", []string{testPodCIDR, "fd00:10::/48"}, nil, true),
		Entry("a bare address is rejected", []string{"10.200.0.1"}, nil, false),
		Entry("garbage is rejected", nil, []string{"nope"}, false),
		Entry("a third range is rejected", []string{"10.1.0.0/16", "10.2.0.0/16", "10.3.0.0/16"}, nil, false),
	)

	It("rejects changing the subnets after creation", func() {
		c := newNet([]string{testPodCIDR}, nil)
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())

		c.Spec.Network.PodSubnets = []string{"10.210.0.0/16"}
		Expect(k8sClient.Update(context.Background(), c)).NotTo(Succeed())
	})

	It("accepts an update that leaves the subnets unchanged", func() {
		c := newNet([]string{testPodCIDR}, nil)
		Expect(k8sClient.Create(context.Background(), c)).To(Succeed())

		c.Labels = map[string]string{"touched": testTrue}
		Expect(k8sClient.Update(context.Background(), c)).To(Succeed())
	})
})

func newMachine() *TalosMachine {
	return &TalosMachine{
		GenerateName: "machine-",
		Namespace:    testNamespace,
		Spec: TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: literalCluster},
			Identity:   TalosMachineIdentity{UUID: "u"},
			Endpoint:   literalNodeIP,
		},
	}
}

var _ = Describe("TalosMachine", func() {
	DescribeTable("hostname is an RFC 1123 name",
		func(hostname string, accepted bool) {
			m := newMachine()
			m.Spec.Hostname = hostname

			expectAdmitted(k8sClient.Create(context.Background(), m), accepted)
		},
		Entry("a label", "node-1", true),
		Entry("a subdomain", "node-1.lab.example.com", true),
		Entry("unset", "", true),
		Entry("Japanese text", "てすと", false),
		Entry("upper case", "Node1", false),
		Entry("an underscore", "node_1", false),
		Entry("a leading hyphen", "-node", false),
		Entry("a trailing dot", "node.", false),
		Entry("a space", "node 1", false),
	)

	DescribeTable("install.disk",
		func(disk *TalosMachineDisk, accepted bool) {
			m := newMachine()
			m.Spec.Install = &TalosMachineInstall{Disk: *disk}

			expectAdmitted(k8sClient.Create(context.Background(), m), accepted)
		},
		Entry("by WWID", &TalosMachineDisk{WWID: "naa.5000c500a1b2c3d4"}, true),
		Entry("by serial", &TalosMachineDisk{Serial: "S3Z9NX0M"}, true),
		Entry("by by-id path", &TalosMachineDisk{ByIDPath: "/dev/disk/by-id/nvme-Samsung_SSD_980_S64DNX0"}, true),
		Entry("by bus path", &TalosMachineDisk{BusPath: "/pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0"}, true),
		Entry("by device path", &TalosMachineDisk{DevPath: "/dev/vda"}, true),
		Entry("no selector", &TalosMachineDisk{}, false),
		Entry("two selectors", &TalosMachineDisk{WWID: "w", Serial: "s"}, false),
		Entry("a by-id path outside /dev/disk/by-id", &TalosMachineDisk{ByIDPath: "/tmp/x"}, false),
		Entry("a device path with a quote", &TalosMachineDisk{DevPath: "/dev/sd\"a"}, false),
	)

	// Spec.network.
	const (
		nicMAC         = "aa:bb:cc:dd:ee:01"
		testStaticAddr = "192.168.1.10/24"
	)

	DescribeTable("network",
		func(n *TalosMachineNetwork, accepted bool) {
			m := newMachine()
			m.Spec.Network = n

			expectAdmitted(k8sClient.Create(context.Background(), m), accepted)
		},
		Entry("dhcp by default", &TalosMachineNetwork{MAC: nicMAC}, true),
		Entry("dhcp with DNS servers", &TalosMachineNetwork{MAC: nicMAC, DNSServers: []string{"1.1.1.1"}}, true),
		Entry("static with a gateway", &TalosMachineNetwork{
			MAC: nicMAC, Addressing: MachineAddressingStatic, Addresses: []string{testStaticAddr}, Gateway: "192.168.1.1",
		}, true),
		Entry("static IPv6", &TalosMachineNetwork{MAC: nicMAC, Addressing: MachineAddressingStatic, Addresses: []string{"fd00::10/64"}}, true),
		Entry("static on a VLAN", &TalosMachineNetwork{
			MAC: nicMAC, Addressing: MachineAddressingStatic, Addresses: []string{"10.0.0.2/24"}, VLANID: 100,
		}, true),
		Entry("dhcp on a VLAN", &TalosMachineNetwork{MAC: nicMAC, VLANID: 4094}, true),
		Entry("static without addresses", &TalosMachineNetwork{MAC: nicMAC, Addressing: MachineAddressingStatic}, false),
		Entry("dhcp with addresses", &TalosMachineNetwork{MAC: nicMAC, Addresses: []string{testStaticAddr}}, false),
		Entry("dhcp with a gateway", &TalosMachineNetwork{MAC: nicMAC, Gateway: "192.168.1.1"}, false),
		Entry("an address without a prefix", &TalosMachineNetwork{
			MAC: nicMAC, Addressing: MachineAddressingStatic, Addresses: []string{"192.168.1.10"},
		}, false),
		Entry("a bad gateway", &TalosMachineNetwork{
			MAC: nicMAC, Addressing: MachineAddressingStatic, Addresses: []string{testStaticAddr}, Gateway: "gw",
		}, false),
		Entry("a bad DNS server", &TalosMachineNetwork{MAC: nicMAC, DNSServers: []string{"dns.example.com"}}, false),
		Entry("a malformed MAC", &TalosMachineNetwork{MAC: "aa-bb-cc-dd-ee-01"}, false),
		Entry("a missing MAC", &TalosMachineNetwork{}, false),
		Entry("a negative VLAN", &TalosMachineNetwork{MAC: nicMAC, VLANID: -1}, false),
		Entry("VLAN 4095", &TalosMachineNetwork{MAC: nicMAC, VLANID: 4095}, false),
		Entry("an unknown addressing", &TalosMachineNetwork{MAC: nicMAC, Addressing: "slaac"}, false),
	)

	// Spec.disks.
	DescribeTable("disks",
		func(disks []TalosMachineDiskRole, accepted bool) {
			m := newMachine()
			m.Spec.Disks = disks

			expectAdmitted(k8sClient.Create(context.Background(), m), accepted)
		},
		Entry("ephemeral", []TalosMachineDiskRole{{Role: MachineDiskRoleEphemeral, Disk: TalosMachineDisk{Serial: "E1"}}}, true),
		Entry("a user volume", []TalosMachineDiskRole{
			{Role: MachineDiskRoleUserVolume, Name: "data", MaxSize: "200GiB", Disk: TalosMachineDisk{WWID: "w1"}},
		}, true),
		Entry("ephemeral and two user volumes", []TalosMachineDiskRole{
			{Role: MachineDiskRoleEphemeral, Disk: TalosMachineDisk{Serial: "E1"}},
			{Role: MachineDiskRoleUserVolume, Name: "a", Disk: TalosMachineDisk{Serial: "A"}},
			{Role: MachineDiskRoleUserVolume, Name: "b", Disk: TalosMachineDisk{Serial: "B"}},
		}, true),
		Entry("two ephemeral disks", []TalosMachineDiskRole{
			{Role: MachineDiskRoleEphemeral, Disk: TalosMachineDisk{Serial: "E1"}},
			{Role: MachineDiskRoleEphemeral, Disk: TalosMachineDisk{Serial: "E2"}},
		}, false),
		Entry("duplicate user volume names", []TalosMachineDiskRole{
			{Role: MachineDiskRoleUserVolume, Name: "a", Disk: TalosMachineDisk{Serial: "A"}},
			{Role: MachineDiskRoleUserVolume, Name: "a", Disk: TalosMachineDisk{Serial: "B"}},
		}, false),
		Entry("a user volume without a name", []TalosMachineDiskRole{
			{Role: MachineDiskRoleUserVolume, Disk: TalosMachineDisk{Serial: "A"}},
		}, false),
		Entry("a name on ephemeral", []TalosMachineDiskRole{
			{Role: MachineDiskRoleEphemeral, Name: "x", Disk: TalosMachineDisk{Serial: "A"}},
		}, false),
		Entry("a size on ephemeral", []TalosMachineDiskRole{
			{Role: MachineDiskRoleEphemeral, MaxSize: "10GiB", Disk: TalosMachineDisk{Serial: "A"}},
		}, false),
		Entry("a bad volume name", []TalosMachineDiskRole{
			{Role: MachineDiskRoleUserVolume, Name: "my data", Disk: TalosMachineDisk{Serial: "A"}},
		}, false),
		Entry("a bad size", []TalosMachineDiskRole{
			{Role: MachineDiskRoleUserVolume, Name: "a", MaxSize: "lots", Disk: TalosMachineDisk{Serial: "A"}},
		}, false),
		Entry("an unknown role", []TalosMachineDiskRole{{Role: "storage", Disk: TalosMachineDisk{Serial: "A"}}}, false),
		Entry("a disk with no selector", []TalosMachineDiskRole{{Role: MachineDiskRoleEphemeral}}, false),
	)
})
