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
	corev1 "k8s.io/api/core/v1"
)

// ADR 0123: the enums of spec.security.secureBoot and kernelLockdown.
var _ = Describe("TalosCluster spec.security.secureBoot and kernelLockdown", func() {
	create := func(s *TalosClusterSecurity) error {
		c := newCluster(corev1.LocalObjectReference{Name: literalSecrets}, 3, "true")
		c.Spec.Security = s

		return k8sClient.Create(context.Background(), c)
	}

	sb := func(mode string) *TalosClusterSecurity {
		return &TalosClusterSecurity{SecureBoot: &TalosClusterSecureBoot{Mode: mode}}
	}

	DescribeTable("modes and levels",
		func(s *TalosClusterSecurity, accepted bool) {
			expectAdmitted(create(s), accepted)
		},
		Entry("Off", sb(SecureBootOff), true),
		Entry("Preferred", sb(SecureBootPreferred), true),
		Entry("Required", sb(SecureBootRequired), true),
		Entry("a mode is required inside secureBoot", &TalosClusterSecurity{SecureBoot: &TalosClusterSecureBoot{}}, false),
		Entry("an unknown mode is rejected", sb("Maybe"), false),
		Entry("lowercase is rejected", sb("required"), false),
		Entry("lockdown integrity", &TalosClusterSecurity{KernelLockdown: KernelLockdownIntegrity}, true),
		Entry("lockdown confidentiality", &TalosClusterSecurity{KernelLockdown: KernelLockdownConfidentiality}, true),
		Entry("lockdown none is not something to ask for", &TalosClusterSecurity{KernelLockdown: KernelLockdownNone}, false),
	)
})
