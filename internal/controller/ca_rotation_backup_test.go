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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

var _ = Describe("BundleBackupStale", func() {
	// An export that holds only the CAs from before the rotation is stale whichever CA was replaced: with the etcd CA
	// alone, the Talos and Kubernetes CAs did not change.
	for name, targets := range map[string][]string{
		"the Talos and Kubernetes CAs": nil, "the etcd CA": {ichorv1alpha1.CARotationTargetEtcd},
	} {
		It("stays up after a rotation of "+name+" until the exported bundle is the live one, then goes down", func() {
			f := newCARotFixture()
			f.targets = targets
			f.runUntil(f.newOp(), completed)

			key := types.NamespacedName{Namespace: tmNamespace, Name: f.cluster.Name}
			r := &CertExpiryReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}

			stale := func() bool {
				var got ichorv1alpha1.TalosCluster
				Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				return apimeta.IsStatusConditionTrue(got.Status.Conditions, ichorv1alpha1.ConditionBundleBackupStale)
			}

			setExported := func(fingerprint string) {
				var got ichorv1alpha1.TalosCluster
				Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

				if got.Annotations == nil {
					got.Annotations = map[string]string{}
				}

				got.Annotations[ichorv1alpha1.BundleExportedAnnotation] = fingerprint
				Expect(k8sClient.Update(ctx, &got)).To(Succeed())

				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(stale()).To(BeTrue())

			// The copy made before the rotation does not clear it.
			oldFP, err := secretsbundle.ExportFingerprint(f.old, nil)
			Expect(err).NotTo(HaveOccurred())
			setExported(oldFP)
			Expect(stale()).To(BeTrue())

			liveFP, err := secretsbundle.ExportFingerprint(f.storedBundle(), nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(liveFP).NotTo(Equal(oldFP))

			setExported(liveFP)
			Expect(stale()).To(BeFalse())
		})
	}
})
