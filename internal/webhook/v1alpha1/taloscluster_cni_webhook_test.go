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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("TalosCluster Webhook: removed CNI presets (ADR 0099)", func() {
	v := &TalosClusterCustomValidator{}

	withPreset := func(p string) *ichorv1alpha1.TalosCluster {
		c := newTestCluster()
		c.Spec.CNI.Preset = p

		return c
	}

	It("refuses to create a flannel or custom cluster, pointing at the ADR", func() {
		for _, p := range []string{"flannel", "custom"} {
			_, err := v.ValidateCreate(ctx, withPreset(p))
			Expect(err).To(MatchError(ContainSubstring("ADR 0099")))
		}
	})

	It("creates a cilium cluster, with the preset set or defaulted", func() {
		for _, p := range []string{"cilium", ""} {
			_, err := v.ValidateCreate(ctx, withPreset(p))
			Expect(err).NotTo(HaveOccurred())
		}
	})

	It("refuses a spec change to a stored flannel cluster but not a metadata change", func() {
		old := withPreset("flannel")

		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = testK8sVersion
		_, err := v.ValidateUpdate(ctx, old, changed)
		Expect(err).To(MatchError(ContainSubstring("ADR 0099")))

		labelled := old.DeepCopy()
		labelled.Labels = map[string]string{"a": "b"}
		_, err = v.ValidateUpdate(ctx, old, labelled)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("TalosCluster Webhook: unmanaged CNI preset (ADR 0103)", func() {
	v := &TalosClusterCustomValidator{}

	unmanaged := func(annotations map[string]string) *ichorv1alpha1.TalosCluster {
		c := newTestCluster()
		c.Spec.CNI.Preset = "unmanaged"
		c.Annotations = annotations

		return c
	}

	It("refuses to create an unmanaged cluster without the adoption annotation", func() {
		for _, a := range []map[string]string{nil, {ichorv1alpha1.TalosClusterAdoptedAnnotation: "no"}} {
			_, err := v.ValidateCreate(ctx, unmanaged(a))
			Expect(err).To(MatchError(ContainSubstring(ichorv1alpha1.TalosClusterAdoptedAnnotation)))
		}
	})

	adopted := map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: strings.ToLower("TRUE")}

	It("creates an unmanaged cluster that carries the adoption annotation", func() {
		_, err := v.ValidateCreate(ctx, unmanaged(adopted))
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not treat unmanaged as a removed preset on update", func() {
		old := unmanaged(adopted)
		changed := old.DeepCopy()
		changed.Spec.KubernetesVersion = testK8sVersion
		_, err := v.ValidateUpdate(ctx, old, changed)
		Expect(err).NotTo(HaveOccurred())
	})
})
