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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0129: a hand-written TalosCluster gets the Cilium values checks the UI and the CLI make
// (pkg/cni/cilium ValidateValues, the part that needs nothing outside the object).
var _ = Describe("TalosCluster Webhook: Cilium values (ADR 0129)", func() {
	v := &TalosClusterCustomValidator{}

	withValues := func(raw string) *ichorv1alpha1.TalosCluster {
		c := newTestCluster()
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(raw)}}

		return c
	}

	const (
		valid       = `{"debug":{"enabled":true}}`
		unknownKey  = `{"bpf":{"masqurade":true}}`
		ownedKey    = `{"ipam":{"mode":"cluster-pool"}}`
		schemaType  = `{"operator":{"replicas":"two"}}`
		notAnObject = `["debug"]`
	)

	It("creates a cluster with valid values, or with none", func() {
		_, err := v.ValidateCreate(ctx, withValues(valid))
		Expect(err).NotTo(HaveOccurred())

		_, err = v.ValidateCreate(ctx, newTestCluster())
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("refuses to create a cluster whose values the UI would refuse",
		func(raw string, want string) {
			_, err := v.ValidateCreate(ctx, withValues(raw))
			Expect(err).To(MatchError(ContainSubstring("spec.cni.cilium.values")))
			Expect(err).To(MatchError(ContainSubstring(want)))
		},
		Entry("an unknown key, with a suggestion", unknownKey, "did you mean bpf.masquerade?"),
		Entry("a key Ichor owns", ownedKey, "ipam.mode is set by Ichor"),
		Entry("a schema violation", schemaType, "chart schema"),
		Entry("a value that is not an object", notAnObject, "object"),
		Entry("a layer over the size cap", `{"nodeSelector":{"x":"`+strings.Repeat("a", 70<<10)+`"}}`, "byte limit"),
	)

	It("reports every problem together", func() {
		_, err := v.ValidateCreate(ctx, withValues(`{"bpf":{"masqurade":true},"ipam":{"mode":"cluster-pool"}}`))
		Expect(err).To(MatchError(And(ContainSubstring("bpf.masqurade"), ContainSubstring("ipam.mode"))))
	})

	It("refuses an update that changes the values to invalid ones, and accepts a valid change", func() {
		old := withValues(valid)

		bad := old.DeepCopy()
		bad.Spec.CNI.Cilium.Values.Raw = []byte(unknownKey)
		_, err := v.ValidateUpdate(ctx, old, bad)
		Expect(err).To(MatchError(ContainSubstring("unknown key bpf.masqurade")))

		fine := old.DeepCopy()
		fine.Spec.CNI.Cilium.Values.Raw = []byte(`{"debug":{"enabled":false}}`)
		_, err = v.ValidateUpdate(ctx, old, fine)
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses an update that adds invalid values to a cluster that had none", func() {
		old := newTestCluster()
		_, err := v.ValidateUpdate(ctx, old, withValues(ownedKey))
		Expect(err).To(MatchError(ContainSubstring("ipam.mode")))
	})

	It("does not block an unrelated update of a stored cluster whose values are invalid (ratcheting)", func() {
		old := withValues(unknownKey)

		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = testK8sVersion
		changed.Labels = map[string]string{"a": "b"}
		_, err := v.ValidateUpdate(ctx, old, changed)
		Expect(err).NotTo(HaveOccurred())
	})

	It("checks the stored values again when the kube-proxy mode or the tunnel changes", func() {
		for _, edit := range []func(*ichorv1alpha1.TalosCluster){
			func(c *ichorv1alpha1.TalosCluster) { c.Spec.CNI.KubeProxy = "replaced" },
			func(c *ichorv1alpha1.TalosCluster) { c.Spec.CNI.Tunnel = "geneve" },
		} {
			old := withValues(unknownKey)
			changed := old.DeepCopy()
			edit(changed)
			_, err := v.ValidateUpdate(ctx, old, changed)
			Expect(err).To(MatchError(ContainSubstring("unknown key bpf.masqurade")))
		}
	})

	It("lets the values be cleared", func() {
		old := withValues(unknownKey)
		cleared := old.DeepCopy()
		cleared.Spec.CNI.Cilium.Values = nil
		_, err := v.ValidateUpdate(ctx, old, cleared)
		Expect(err).NotTo(HaveOccurred())
	})

	It("leaves an adopted cluster whose CNI is unmanaged alone", func() {
		c := withValues(ownedKey)
		c.Spec.CNI.Preset = presetUnmanaged
		c.Annotations = map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: annotationTrue}
		_, err := v.ValidateCreate(ctx, c)
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not enforce the guards that need the machine configuration or the live cluster", func() {
		// bpf.masquerade against forwardKubeDNSToHost, KubePrism and the Gateway API CRDs stay with the UI and CLI.
		_, err := v.ValidateCreate(ctx, withValues(`{"bpf":{"masquerade":true},"gatewayAPI":{"enabled":true}}`))
		Expect(err).NotTo(HaveOccurred())
	})
})
