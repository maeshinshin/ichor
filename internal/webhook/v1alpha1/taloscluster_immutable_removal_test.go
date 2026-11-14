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
