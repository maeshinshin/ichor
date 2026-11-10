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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/oidctrust"
)

const (
	itNamespace = "ichor-oidc-it"
	itService   = "ichor-ui-service"
	itHash      = `{"v":1,"hash":"$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA","notBefore":"2026-01-01T00:00:00Z","mustChange":false}`
)

var _ = Describe("IssuerTrustReconciler", func() {
	var (
		clock time.Time
		r     *IssuerTrustReconciler
	)

	reconcile := func() {
		_, err := r.Reconcile(ctx, ctrl.Request{})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}

	get := func(name string, obj client.Object) {
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Namespace: itNamespace, Name: name}, obj)).To(Succeed())
	}

	keys := func() oidctrust.Keys {
		var sec corev1.Secret
		get(oidctrust.SigningSecret, &sec)

		ks, err := oidctrust.ParseKeys(sec.Data[oidctrust.KeysKey])
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		return ks
	}

	// The previous spec's Service may still hold the ClusterIP for a moment after its delete.
	service := func(ip string) {
		EventuallyWithOffset(1, func() error {
			return k8sClient.Create(ctx, &corev1.Service{
				Namespace: itNamespace, Name: itService,
				Spec: corev1.ServiceSpec{
					ClusterIP: ip, Ports: []corev1.ServicePort{{Name: "https", Port: oidctrust.InternalPort}},
				},
			})
		}, "20s", "200ms").Should(Succeed())
	}

	BeforeEach(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			Name: itNamespace,
		}))).To(Succeed())

		clock = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		r = &IssuerTrustReconciler{
			Client: k8sClient, APIReader: k8sClient, Namespace: itNamespace, UIServiceName: itService,
			UserIssuer: true, Now: func() time.Time { return clock },
		}

		service("10.0.0.50")
		DeferCleanup(func() {
			for _, o := range []client.Object{
				&corev1.Service{Namespace: itNamespace, Name: itService},
				&corev1.Secret{Namespace: itNamespace, Name: oidctrust.SigningSecret},
				&corev1.Secret{Namespace: itNamespace, Name: oidctrust.CASecret},
				&corev1.Secret{Namespace: itNamespace, Name: oidctrust.TLSSecret},
				&corev1.Secret{Namespace: itNamespace, Name: oidctrust.CredentialsSecret},
				&corev1.ConfigMap{Namespace: itNamespace, Name: oidctrust.TrustConfigMap},
				&corev1.ConfigMap{Namespace: itNamespace, Name: oidctrust.UICAName},
				&corev1.Secret{Namespace: itNamespace, Name: oidctrust.UICAName},
			} {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, o))).To(Succeed())
			}

			Expect(k8sClient.DeleteAllOf(ctx, &ichorv1alpha1.IchorUser{}, client.InNamespace(itNamespace))).To(Succeed())
		})
	})

	It("creates the keys, the CA, the leaf and the trust ConfigMap, then leaves them alone", func() {
		reconcile()

		Expect(keys().Keys).To(HaveLen(1))

		var cm corev1.ConfigMap
		get(oidctrust.TrustConfigMap, &cm)
		Expect(cm.Data).To(HaveLen(2))
		Expect(cm.Data[oidctrust.DiscoveryURLKey]).To(Equal("https://10.0.0.50:8444/.well-known/openid-configuration"))

		var ca, leaf corev1.Secret
		get(oidctrust.CASecret, &ca)
		get(oidctrust.TLSSecret, &leaf)
		Expect(cm.Data[oidctrust.CAKey]).To(Equal(string(ca.Data[oidctrust.CertKey])))
		Expect(leaf.Data[oidctrust.CAKey]).To(Equal(ca.Data[oidctrust.CertKey]))

		cert, err := decodeCert(leaf.Data[oidctrust.CertKey])
		Expect(err).NotTo(HaveOccurred())
		Expect(cert.IPAddresses).To(HaveLen(2))
		Expect(cert.IPAddresses[0].String()).To(Equal("10.0.0.50"))
		Expect(cert.IPAddresses[1].String()).To(Equal("127.0.0.1"))
		Expect(cert.DNSNames).To(ConsistOf("ichor-ui-service", "ichor-ui-service.ichor-oidc-it", "ichor-ui-service.ichor-oidc-it.svc",
			"ichor-ui-service.ichor-oidc-it.svc.cluster.local", "localhost"))

		var caAgain, leafAgain corev1.Secret
		reconcile()
		get(oidctrust.CASecret, &caAgain)
		get(oidctrust.TLSSecret, &leafAgain)
		Expect(caAgain.ResourceVersion).To(Equal(ca.ResourceVersion))
		Expect(leafAgain.ResourceVersion).To(Equal(leaf.ResourceVersion))
	})

	Context("without --user-issuer", func() {
		BeforeEach(func() { r.UserIssuer = false })

		It("issues the leaf and publishes ichor-ui-ca, and nothing of the issuer", func() {
			reconcile()

			var ca, leaf, sec corev1.Secret
			get(oidctrust.CASecret, &ca)
			get(oidctrust.TLSSecret, &leaf)
			get(oidctrust.UICAName, &sec)

			var cm corev1.ConfigMap
			get(oidctrust.UICAName, &cm)

			Expect(cm.Data).To(HaveLen(1))
			Expect(cm.Data[oidctrust.CAKey]).To(Equal(string(ca.Data[oidctrust.CertKey])))
			Expect(sec.Data).To(HaveLen(1))
			Expect(sec.Data[oidctrust.CAKey]).To(Equal(ca.Data[oidctrust.CertKey]))

			// The CA key appears in neither published object.
			Expect(cm.BinaryData).To(BeEmpty())
			Expect(string(sec.Data[oidctrust.CAKey])).NotTo(ContainSubstring("PRIVATE KEY"))
			Expect(cm.Data[oidctrust.CAKey]).NotTo(ContainSubstring("PRIVATE KEY"))

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: itNamespace, Name: oidctrust.SigningSecret}, &corev1.Secret{})
			Expect(client.IgnoreNotFound(err)).To(Succeed())
			Expect(err).To(HaveOccurred())
		})

		It("adds the host of the external URL, and renews the leaf at two thirds of its life", func() {
			r.ExternalURL = "https://ichor.example.com:8443/"
			reconcile()

			var leaf corev1.Secret
			get(oidctrust.TLSSecret, &leaf)

			cert, err := decodeCert(leaf.Data[oidctrust.CertKey])
			Expect(err).NotTo(HaveOccurred())
			Expect(cert.DNSNames).To(ContainElement("ichor.example.com"))

			clock = clock.Add(issuerLeafLifetime*2/3 + time.Hour)
			reconcile()

			var renewed corev1.Secret
			get(oidctrust.TLSSecret, &renewed)
			Expect(renewed.Data[oidctrust.CertKey]).NotTo(Equal(leaf.Data[oidctrust.CertKey]))
		})
	})

	It("rotates the signing key every 30 days and drops the old one after its last token", func() {
		reconcile()

		first := keys().Keys[0]

		clock = clock.Add(issuerRotateEvery)
		reconcile()

		ks := keys()
		Expect(ks.Keys).To(HaveLen(2))

		signing, ok := ks.Signing(clock)
		Expect(ok).To(BeTrue())
		Expect(signing.KID).To(Equal(first.KID))

		clock = clock.Add(issuerActivationDelay)
		signing, _ = keys().Signing(clock)
		Expect(signing.KID).NotTo(Equal(first.KID))

		clock = clock.Add(DefaultRefreshLifetime + issuerRetireGrace)
		reconcile()
		Expect(keys().Keys).To(HaveLen(1))
		Expect(keys().Keys[0].KID).NotTo(Equal(first.KID))
	})

	It("reissues only the leaf and the trust ConfigMap when the ClusterIP changes", func() {
		reconcile()

		var ca corev1.Secret
		get(oidctrust.CASecret, &ca)

		Expect(k8sClient.Delete(ctx, &corev1.Service{
			Namespace: itNamespace, Name: itService,
		})).To(Succeed())
		service("10.0.0.51")
		reconcile()

		var caAfter, leaf corev1.Secret
		get(oidctrust.CASecret, &caAfter)
		Expect(caAfter.ResourceVersion).To(Equal(ca.ResourceVersion))

		get(oidctrust.TLSSecret, &leaf)

		cert, err := decodeCert(leaf.Data[oidctrust.CertKey])
		Expect(err).NotTo(HaveOccurred())
		Expect(cert.IPAddresses[0].String()).To(Equal("10.0.0.51"))

		var cm corev1.ConfigMap
		get(oidctrust.TrustConfigMap, &cm)
		Expect(cm.Data[oidctrust.DiscoveryURLKey]).To(Equal("https://10.0.0.51:8444/.well-known/openid-configuration"))
	})

	It("does not replace a signing document it cannot read", func() {
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Namespace: itNamespace, Name: oidctrust.SigningSecret,
			Data: map[string][]byte{oidctrust.KeysKey: []byte("{broken")},
		})).To(Succeed())

		_, err := r.Reconcile(ctx, ctrl.Request{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("broken"))

		var sec corev1.Secret
		get(oidctrust.SigningSecret, &sec)
		Expect(string(sec.Data[oidctrust.KeysKey])).To(Equal("{broken"))
	})

	It("writes IchorUser status and removes credentials of deleted users after the grace period", func() {
		user := func(name string, disabled bool) {
			Expect(k8sClient.Create(ctx, &ichorv1alpha1.IchorUser{
				Namespace: itNamespace, Name: name,
				Spec: ichorv1alpha1.IchorUserSpec{Disabled: disabled},
			})).To(Succeed())
		}
		user("alice", false)
		user("bob", false)
		user("carol", true)

		Expect(k8sClient.Create(ctx, &corev1.Secret{
			Namespace: itNamespace, Name: oidctrust.CredentialsSecret,
			Data: map[string][]byte{
				"alice": []byte(itHash), "carol": []byte(itHash), "ghost": []byte(itHash), "junk": []byte("not json"),
			},
		})).To(Succeed())

		reconcile()

		cond := func(name, typ string) metav1.Condition {
			var u ichorv1alpha1.IchorUser
			get(name, &u)

			c := apimeta.FindStatusCondition(u.Status.Conditions, typ)
			ExpectWithOffset(1, c).NotTo(BeNil())
			ExpectWithOffset(1, c.Message).NotTo(ContainSubstring("argon2"))

			return *c
		}

		Expect(cond("alice", ichorv1alpha1.IchorUserConditionPasswordSet).Status).To(Equal(metav1.ConditionTrue))
		Expect(cond("alice", ichorv1alpha1.IchorUserConditionReady).Status).To(Equal(metav1.ConditionTrue))
		Expect(cond("bob", ichorv1alpha1.IchorUserConditionPasswordSet).Status).To(Equal(metav1.ConditionFalse))
		Expect(cond("bob", ichorv1alpha1.IchorUserConditionReady).Reason).To(Equal("NoPassword"))
		Expect(cond("carol", ichorv1alpha1.IchorUserConditionReady).Reason).To(Equal("Disabled"))

		// First sighting only starts the grace clock.
		var creds corev1.Secret
		get(oidctrust.CredentialsSecret, &creds)
		Expect(creds.Data).To(HaveKey("ghost"))

		clock = clock.Add(issuerOrphanGrace - time.Second)
		reconcile()
		get(oidctrust.CredentialsSecret, &creds)
		Expect(creds.Data).To(HaveKey("ghost"))

		clock = clock.Add(time.Second)
		reconcile()
		get(oidctrust.CredentialsSecret, &creds)
		Expect(creds.Data).NotTo(HaveKey("ghost"))
		Expect(creds.Data).NotTo(HaveKey("junk"))
		Expect(creds.Data).To(HaveKey("alice"))
		Expect(creds.Data).To(HaveKey("carol"))
	})
})
