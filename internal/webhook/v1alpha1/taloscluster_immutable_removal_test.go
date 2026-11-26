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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const ciliumPreset = "cilium"

// The CEL transition rules on spec.network and spec.cni do not run when the field is removed, so
// the webhook refusal is the only guard against removal.
var _ = Describe("TalosCluster Webhook: removing an immutable field", func() {
	v := &TalosClusterCustomValidator{}

	withNetworkAndCNI := func() *ichorv1alpha1.TalosCluster {
		c := newTestCluster()
		c.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{"10.244.0.0/16"}}
		c.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: ciliumPreset}

		return c
	}

	It("refuses to remove spec.network or spec.cni once set", func() {
		old := withNetworkAndCNI()

		noNetwork := old.DeepCopy()
		noNetwork.Spec.Network = nil
		_, err := v.ValidateUpdate(ctx, old, noNetwork)
		Expect(err).To(MatchError(ContainSubstring("spec.network is fixed once the cluster exists")))

		noCNI := old.DeepCopy()
		noCNI.Spec.CNI = ichorv1alpha1.TalosClusterCNI{}
		_, err = v.ValidateUpdate(ctx, old, noCNI)
		Expect(err).To(MatchError(ContainSubstring("spec.cni is fixed once the cluster exists")))
	})

	It("allows setting spec.network or spec.cni on a cluster that had neither", func() {
		_, err := v.ValidateUpdate(ctx, newTestCluster(), withNetworkAndCNI())
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows an update that leaves both fields as they are", func() {
		old := withNetworkAndCNI()

		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = testK8sVersion
		_, err := v.ValidateUpdate(ctx, old, changed)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("TalosCluster Webhook: immutable fields through the API server", func() {
	var obj *ichorv1alpha1.TalosCluster

	BeforeEach(func() {
		obj = newTestCluster()
		obj.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{"10.244.0.0/16"}}
		obj.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: ciliumPreset}
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	})

	AfterEach(func() { forceDelete(obj) })

	removeField := func(path string) error {
		return k8sClient.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, []byte(`[{"op":"remove","path":"`+path+`"}]`)))
	}

	It("refuses a JSON patch that removes spec.network", func() {
		err := removeField("/spec/network")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.network is fixed once the cluster exists"))
	})

	It("admits a JSON patch that removes spec.cni of a cilium cluster, because the CRD default restores it", func() {
		Expect(removeField("/spec/cni")).To(Succeed())
	})

	It("still refuses a different spec.network value", func() {
		obj.Spec.Network.PodSubnets = []string{"10.245.0.0/16"}
		err := k8sClient.Update(ctx, obj)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("network cannot change after it is set"))
	})
})

var _ = Describe("TalosCluster Webhook: spec.cni through the API server", func() {
	var obj *ichorv1alpha1.TalosCluster

	AfterEach(func() { forceDelete(obj) })

	patch := func(body string) error {
		return k8sClient.Patch(ctx, obj, client.RawPatch(types.JSONPatchType, []byte(body)))
	}

	presetOf := func() string {
		readBack := &ichorv1alpha1.TalosCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), readBack)).To(Succeed())

		return readBack.Spec.CNI.Preset
	}

	Context("on a cilium cluster", func() {
		BeforeEach(func() {
			obj = newTestCluster()
			obj.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: ciliumPreset}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		})

		It("refuses a JSON patch that nulls spec.cni, because the default makes the preset cilium", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":null}]`)).To(MatchError(ContainSubstring("cni.preset cannot change")))
		})

		It("admits a JSON patch that replaces spec.cni with {}, because the preset default restores cilium", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(Succeed())
			Expect(presetOf()).To(Equal(ciliumPreset))
		})

		It("refuses a JSON patch that empties the preset", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni/preset","value":""}]`)).To(MatchError(ContainSubstring("Unsupported value")))
		})
	})

	Context("on a cilium cluster that holds Cilium values", func() {
		const values = `{"hubble":{"enabled":true},"operator":{"replicas":1}}`

		BeforeEach(func() {
			obj = newTestCluster()
			obj.Spec.CNI = ichorv1alpha1.TalosClusterCNI{
				Preset: ciliumPreset, Cilium: &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(values)}},
			}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		})

		valuesOf := func() string {
			readBack := &ichorv1alpha1.TalosCluster{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), readBack)).To(Succeed())

			if readBack.Spec.CNI.Cilium == nil || readBack.Spec.CNI.Cilium.Values == nil {
				return ""
			}

			return string(readBack.Spec.CNI.Cilium.Values.Raw)
		}

		It("refuses a JSON patch that replaces spec.cni with {}, which the defaults would turn into a stanza with no values", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(MatchError(ContainSubstring("would drop spec.cni.cilium")))
			Expect(valuesOf()).To(MatchJSON(values))
		})

		It("refuses an update that sends spec.cni without cilium", func() {
			obj.Spec.CNI = ichorv1alpha1.TalosClusterCNI{}
			Expect(k8sClient.Update(ctx, obj)).To(MatchError(ContainSubstring("would drop spec.cni.cilium")))
			Expect(valuesOf()).To(MatchJSON(values))
		})

		It("admits clearing the values on purpose with spec.cni.cilium as {}", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni/cilium","value":{}}]`)).To(Succeed())
			Expect(valuesOf()).To(BeEmpty())
		})

		It("admits an update that leaves spec.cni as it is", func() {
			obj.Spec.KubernetesVersion = testK8sVersion
			Expect(k8sClient.Update(ctx, obj)).To(Succeed())
			Expect(valuesOf()).To(MatchJSON(values))
		})
	})

	Context("on an unmanaged cluster", func() {
		BeforeEach(func() {
			obj = newTestCluster()
			obj.Annotations = map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: annotationTrue}
			obj.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: presetUnmanaged}
			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		})

		It("refuses a JSON patch that replaces spec.cni with {}, because the default makes the preset cilium", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(MatchError(ContainSubstring("cni.preset cannot change")))
		})

		It("refuses a JSON patch that nulls spec.cni, because the default makes the preset cilium", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":null}]`)).To(MatchError(ContainSubstring("cni.preset cannot change")))
		})
	})
})
