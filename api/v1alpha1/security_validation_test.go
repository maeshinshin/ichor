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
)

// Spec.security is opt-in; the CRD rejects the shapes that would lock a cluster out.
var _ = Describe("TalosCluster spec.security", func() {
	create := func(s *TalosClusterSecurity) error {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Security = s

		return k8sClient.Create(context.Background(), c)
	}

	fw := func(mut func(*TalosClusterFirewall)) *TalosClusterSecurity {
		f := &TalosClusterFirewall{Enabled: true, ControllerCIDRs: []string{"192.168.10.0/24"}, NodeCIDRs: []string{"10.0.0.0/24"}}
		if mut != nil {
			mut(f)
		}

		return &TalosClusterSecurity{Firewall: f}
	}

	It("accepts a cluster with no security at all, and an empty one", func() {
		Expect(create(nil)).To(Succeed())
		Expect(create(&TalosClusterSecurity{})).To(Succeed())
	})

	DescribeTable("firewall",
		func(s *TalosClusterSecurity, accepted bool) {
			if accepted {
				Expect(create(s)).To(Succeed())
			} else {
				Expect(create(s)).NotTo(Succeed())
			}
		},
		Entry("a complete firewall is accepted", fw(nil), true),
		Entry("disabled needs no sources", &TalosClusterSecurity{Firewall: &TalosClusterFirewall{}}, true),
		Entry("enabled without controllerCIDRs is rejected", fw(func(f *TalosClusterFirewall) { f.ControllerCIDRs = nil }), false),
		Entry("enabled without nodeCIDRs is rejected", fw(func(f *TalosClusterFirewall) { f.NodeCIDRs = nil }), false),
		Entry("a /0 controller source is rejected", fw(func(f *TalosClusterFirewall) { f.ControllerCIDRs = []string{"0.0.0.0/0"} }), false),
		Entry("a bad CIDR is rejected", fw(func(f *TalosClusterFirewall) { f.NodeCIDRs = []string{"nope"} }), false),
		Entry("an extra apiserver source of /0 is accepted", fw(func(f *TalosClusterFirewall) { f.APIServerCIDRs = []string{"0.0.0.0/0"} }), true),
		Entry("an allow rule is accepted", fw(func(f *TalosClusterFirewall) {
			f.Allow = []TalosClusterFirewallRule{{Name: "ingress", Protocol: "tcp", Ports: []string{"80", "8000-8100"}, CIDRs: []string{"0.0.0.0/0"}}}
		}), true),
		Entry("an allow rule with a bad port is rejected", fw(func(f *TalosClusterFirewall) {
			f.Allow = []TalosClusterFirewallRule{{Name: "x", Protocol: "tcp", Ports: []string{"http"}, CIDRs: []string{"10.0.0.0/8"}}}
		}), false),
		Entry("an allow rule with protocol icmp is rejected", fw(func(f *TalosClusterFirewall) {
			f.Allow = []TalosClusterFirewallRule{{Name: "x", Protocol: "icmp", Ports: []string{"1"}, CIDRs: []string{"10.0.0.0/8"}}}
		}), false),
		Entry("a duplicate allow name is rejected", fw(func(f *TalosClusterFirewall) {
			r := TalosClusterFirewallRule{Name: "x", Protocol: "tcp", Ports: []string{"1"}, CIDRs: []string{"10.0.0.0/8"}}
			f.Allow = []TalosClusterFirewallRule{r, r}
		}), false),
	)

	DescribeTable("podSecurity, audit and diskEncryption",
		func(s *TalosClusterSecurity, accepted bool) {
			if accepted {
				Expect(create(s)).To(Succeed())
			} else {
				Expect(create(s)).NotTo(Succeed())
			}
		},
		Entry("podSecurity restricted", &TalosClusterSecurity{PodSecurity: &TalosClusterPodSecurity{Enforce: "restricted"}}, true),
		Entry("podSecurity with an unknown level is rejected", &TalosClusterSecurity{PodSecurity: &TalosClusterPodSecurity{Enforce: "strict"}}, false),
		Entry("audit rbacChanges", &TalosClusterSecurity{Audit: &TalosClusterAudit{Policy: "rbacChanges"}}, true),
		Entry("audit without a policy is rejected", &TalosClusterSecurity{Audit: &TalosClusterAudit{}}, false),
		Entry("audit with an unknown preset is rejected", &TalosClusterSecurity{Audit: &TalosClusterAudit{Policy: "all"}}, false),
		Entry("tpm", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "tpm"}}, true),
		Entry("nodeID", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "nodeID"}}, true),
		Entry("kms with an endpoint", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "kms", KMSEndpoint: "https://kms:4443"}}, true),
		Entry("kms without an endpoint is rejected", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "kms"}}, false),
		Entry("an endpoint on tpm is rejected", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "tpm", KMSEndpoint: "https://kms:4443"}}, false),
		Entry("static is not a type", &TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "static"}}, false),
	)

	It("rejects requireSecureBoot on a key type other than tpm", func() {
		yes := true

		Expect(create(&TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "nodeID", RequireSecureBoot: &yes}})).NotTo(Succeed())
		Expect(create(&TalosClusterSecurity{DiskEncryption: &TalosClusterDiskEncryption{Type: "tpm", RequireSecureBoot: &yes}})).To(Succeed())
	})
})
