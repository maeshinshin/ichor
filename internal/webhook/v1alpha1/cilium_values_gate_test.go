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

	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// ADR 0131: spec.cni.cilium.values reaches a Helm Job that can create privileged pods, so changing it
// needs update on talosclusters/cilium-values, which only ichor-admin holds.
var _ = Describe("Cilium values gate on TalosCluster", func() {
	const (
		operator   = "cilium-gate-operator"
		valueAdmin = "cilium-gate-admin"
		valuesA    = `{"debug":{"enabled":true}}`
		valuesB    = `{"debug":{"enabled":false}}`
	)

	withValues := func(raw string) *ichorv1alpha1.TalosCluster {
		c := newTestCluster()
		c.Spec.CNI.Cilium = &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: []byte(raw)}}

		return c
	}

	cleanup := func(c *ichorv1alpha1.TalosCluster) {
		DeferCleanup(func() {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(c), c); err != nil {
				return
			}

			c.Annotations = map[string]string{deleteguard.AllowDeleteAnnotation: deleteguard.AllowDeleteValue}
			Expect(k8sClient.Update(ctx, c)).To(Succeed())
			Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		})
	}

	BeforeEach(func() {
		write := rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{credentialClusters, "talosclusters/secret-references"}, Verbs: []string{verbGet, verbCreate, verbUpdate, verbPatch},
		}
		bindRole("cilium-gate-operator", operator, write)
		bindRole("cilium-gate-admin", valueAdmin, write, rbacv1.PolicyRule{
			APIGroups: []string{ichorGroup}, Resources: []string{"talosclusters/" + ichorv1alpha1.CiliumValuesSubresource}, Verbs: []string{ichorv1alpha1.CiliumValuesVerb},
		})
	})

	It("refuses an operator who creates a cluster with Cilium values", func() {
		err := asUser(operator).Create(ctx, withValues(valuesA))
		Expect(err).To(MatchError(ContainSubstring("talosclusters/cilium-values")))
	})

	It("admits an operator who creates a cluster without values", func() {
		c := newTestCluster()
		Expect(asUser(operator).Create(ctx, c)).To(Succeed())
		cleanup(c)
	})

	It("admits a holder of update on talosclusters/cilium-values creating with values", func() {
		c := withValues(valuesA)
		Expect(asUser(valueAdmin).Create(ctx, c)).To(Succeed())
		cleanup(c)
	})

	Context("on an existing cluster", func() {
		var cluster *ichorv1alpha1.TalosCluster

		BeforeEach(func() {
			cluster = withValues(valuesA)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			cleanup(cluster)
		})

		edit := func(user string, f func(*ichorv1alpha1.TalosCluster)) error {
			c := &ichorv1alpha1.TalosCluster{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			f(c)

			return asUser(user).Update(ctx, c)
		}

		It("refuses an operator who changes the values", func() {
			err := edit(operator, func(c *ichorv1alpha1.TalosCluster) { c.Spec.CNI.Cilium.Values.Raw = []byte(valuesB) })
			Expect(err).To(MatchError(ContainSubstring("talosclusters/cilium-values")))
		})

		It("refuses an operator who removes the values", func() {
			err := edit(operator, func(c *ichorv1alpha1.TalosCluster) { c.Spec.CNI.Cilium = nil })
			Expect(err).To(MatchError(ContainSubstring("talosclusters/cilium-values")))
		})

		It("admits an unrelated edit by an operator when the values are unchanged", func() {
			Expect(edit(operator, func(c *ichorv1alpha1.TalosCluster) { c.Labels = map[string]string{"a": "b"} })).To(Succeed())
		})

		It("admits an operator who rewrites the same values with other spacing", func() {
			Expect(edit(operator, func(c *ichorv1alpha1.TalosCluster) {
				c.Spec.CNI.Cilium.Values.Raw = []byte(`{ "debug": { "enabled": true } }`)
			})).To(Succeed())
		})

		It("admits a holder of the verb who changes the values", func() {
			Expect(edit(valueAdmin, func(c *ichorv1alpha1.TalosCluster) { c.Spec.CNI.Cilium.Values.Raw = []byte(valuesB) })).To(Succeed())
		})
	})
})
