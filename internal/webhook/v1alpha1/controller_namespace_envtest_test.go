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

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The generated webhook configuration sends a create outside the controller's namespace to the check: the request
// goes through the real API server, not into the handler.
var _ = Describe("the controller-namespace webhook, through the API server", func() {
	const elsewhere = "elsewhere"

	It("refuses a TalosCluster created outside the namespace and admits one inside", func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: elsewhere}))).To(Succeed())

		outside := newTestCluster()
		outside.Namespace = elsewhere

		err := k8sClient.Create(ctx, outside)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("vnamespace-v1alpha1.kb.io"))
		Expect(err.Error()).To(ContainSubstring(`"` + testNamespace + `"`))

		inside := newTestCluster()
		inside.Namespace = testNamespace
		Expect(k8sClient.Create(ctx, inside)).To(Succeed())
		DeferCleanup(func() { forceDelete(inside) })
	})
})
