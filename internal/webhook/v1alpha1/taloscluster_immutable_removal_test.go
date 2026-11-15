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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

	It("refuses a JSON patch that removes spec.cni", func() {
		err := removeField("/spec/cni")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spec.cni is fixed once the cluster exists"))
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

		It("refuses a JSON patch that nulls spec.cni", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":null}]`)).To(MatchError(ContainSubstring("spec.cni is fixed once the cluster exists")))
		})

		It("admits a JSON patch that replaces spec.cni with {}, because the preset default restores cilium", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":{}}]`)).To(Succeed())
			Expect(presetOf()).To(Equal(ciliumPreset))
		})

		It("refuses a JSON patch that empties the preset", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni/preset","value":""}]`)).To(MatchError(ContainSubstring("Unsupported value")))
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

		It("refuses a JSON patch that nulls spec.cni", func() {
			Expect(patch(`[{"op":"replace","path":"/spec/cni","value":null}]`)).To(MatchError(ContainSubstring("spec.cni is fixed once the cluster exists")))
		})
	})

	Context("on a cluster created without spec.cni", func() {
		BeforeEach(func() {
			// A Go client always sends spec.cni, so an object without it comes from a raw document. With
			// driftPolicy set, the defaulting webhook adds nothing and the stored object keeps no spec.cni.
			raw, err := json.Marshal(newTestCluster())
			Expect(err).NotTo(HaveOccurred())
			var m map[string]any
			Expect(json.Unmarshal(raw, &m)).To(Succeed())
			m["apiVersion"], m["kind"] = "ichor.maesh.dev/v1alpha1", "TalosCluster"
			delete(m["spec"].(map[string]any), "cni")
			m["spec"].(map[string]any)["driftPolicy"] = "warnOnly"

			u := &unstructured.Unstructured{Object: m}
			Expect(k8sClient.Create(ctx, u)).To(Succeed())

			stored := &unstructured.Unstructured{}
			stored.SetGroupVersionKind(u.GroupVersionKind())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(u), stored)).To(Succeed())
			_, found, _ := unstructured.NestedFieldNoCopy(stored.Object, "spec", "cni")
			Expect(found).To(BeFalse())

			obj = &ichorv1alpha1.TalosCluster{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(u), obj)).To(Succeed())
		})

		It("admits a JSON patch that adds spec.cni as {}, which reads back as cilium", func() {
			Expect(patch(`[{"op":"add","path":"/spec/cni","value":{}}]`)).To(Succeed())
			Expect(presetOf()).To(Equal(ciliumPreset))
		})

		It("refuses a JSON patch that adds spec.cni as unmanaged without the adoption annotation", func() {
			Expect(patch(`[{"op":"add","path":"/spec/cni","value":{"preset":"unmanaged"}}]`)).To(MatchError(ContainSubstring("only for clusters brought in by")))
		})
	})
})
