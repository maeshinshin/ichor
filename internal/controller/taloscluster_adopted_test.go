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

package controller

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("TalosClusterReconciler CA rotation advice", func() {
	advised := func(name string) bool {
		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: name}, &got)).To(Succeed())

		return apimeta.IsStatusConditionTrue(got.Status.Conditions, ichorv1alpha1.ConditionCARotationAdvised)
	}

	It("sets CARotationAdvised on a cluster carrying the adoption annotation, and not on another", func() {
		plain := newTestCluster("advice-plain", "v1.14")
		adopted := newTestCluster("advice-adopted", "v1.14")
		adopted.Annotations = map[string]string{ichorv1alpha1.TalosClusterAdoptedAnnotation: strconv.FormatBool(true)}
		Expect(k8sClient.Update(ctx, adopted)).To(Succeed())

		r := &TalosClusterReconciler{Client: k8sClient}

		_, err := reconcileCluster(r, plain)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconcileCluster(r, adopted)
		Expect(err).NotTo(HaveOccurred())
		// A second pass changes nothing and does not fail.
		_, err = reconcileCluster(r, adopted)
		Expect(err).NotTo(HaveOccurred())

		Expect(advised(plain.Name)).To(BeFalse())
		Expect(advised(adopted.Name)).To(BeTrue())
	})
})
