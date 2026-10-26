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
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func notAfterOf(pemBytes []byte) time.Time {
	block, _ := pem.Decode(pemBytes)
	Expect(block).NotTo(BeNil())
	cert, err := x509.ParseCertificate(block.Bytes)
	Expect(err).NotTo(HaveOccurred())

	return cert.NotAfter
}

var _ = Describe("CertExpiryReconciler", func() {
	It("publishes every CA's expiry in status and as a metric, and no key material", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
		r := &CertExpiryReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(certExpiryRecheck))

		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, key, &got)).To(Succeed())

		want := map[string]time.Time{
			"talos":                 notAfterOf(bundle.Certs.OS.Crt),
			"kubernetes":            notAfterOf(bundle.Certs.K8s.Crt),
			"kubernetes-aggregator": notAfterOf(bundle.Certs.K8sAggregator.Crt),
			"etcd":                  notAfterOf(bundle.Certs.Etcd.Crt),
		}
		Expect(got.Status.Certificates).To(HaveLen(len(want)))

		for _, c := range got.Status.Certificates {
			Expect(want).To(HaveKey(c.Name))
			// metav1.Time keeps whole seconds.
			Expect(c.NotAfter.Unix()).To(Equal(want[c.Name].Unix()), c.Name)
			Expect(testutil.ToFloat64(certExpiry.WithLabelValues(key.Namespace, key.Name, c.Name))).
				To(Equal(float64(want[c.Name].Unix())), c.Name)
		}

		raw := fmt.Sprintf("%v", got.Status)
		for _, secretScalar := range bundleSecretMaterial(bundle) {
			Expect(strings.Contains(raw, secretScalar)).To(BeFalse())
		}
	})

	It("drops a deleted cluster's series", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}
		r := &CertExpiryReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(testutil.CollectAndCount(certExpiry)).To(BeNumerically(">=", 4))

		before := testutil.CollectAndCount(certExpiry)
		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(testutil.CollectAndCount(certExpiry)).To(Equal(before - 4))
	})

	It("retries a missing bundle without failing the reconcile", func() {
		cluster := newTestCluster("no-such-secret", "v1.14")
		r := &CertExpiryReconciler{Client: k8sClient, ControllerNamespace: tmNamespace}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(certExpiryRetry))
	})
})
