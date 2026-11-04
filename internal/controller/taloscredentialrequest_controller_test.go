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
	stdx509 "crypto/x509"
	"encoding/json"
	"encoding/pem"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/credentials"
)

// The controller signs the public key of a request and nothing else of it.
var _ = Describe("TalosCredentialRequest controller", func() {
	var (
		cluster *ichorv1alpha1.TalosCluster
		r       *TalosCredentialRequestReconciler
	)

	BeforeEach(func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster = newTestCluster(secret.Name, "v1.14")
		r = &TalosCredentialRequestReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), ControllerNamespace: tmNamespace}
	})

	request := func(spec ichorv1alpha1.TalosCredentialRequestSpec) *ichorv1alpha1.TalosCredentialRequest {
		spec.ClusterRef = corev1.LocalObjectReference{Name: cluster.Name}
		cr := &ichorv1alpha1.TalosCredentialRequest{GenerateName: "cred-", Namespace: tmNamespace, Spec: spec}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())

		_, err := r.Reconcile(ctx, ctrl.Request{Namespace: cr.Namespace, Name: cr.Name})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, cr)).To(Succeed())

		return cr
	}

	parse := func(crt string) *stdx509.Certificate {
		block, _ := pem.Decode([]byte(crt))
		Expect(block).NotTo(BeNil())

		c, err := stdx509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())

		return c
	}

	It("signs a reader certificate for the requester's key, with the role and the lifetime of the purpose", func() {
		csr, _, err := credentials.NewCSR(false)
		Expect(err).NotTo(HaveOccurred())

		cr := request(ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeNodeReader, CSR: string(csr)})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued), cr.Status.Message)

		crt := parse(cr.Status.Certificate)
		Expect(crt.Subject.Organization).To(ConsistOf("os:reader"))
		Expect(crt.NotAfter).To(BeTemporally("~", time.Now().Add(credentials.DialLifetime), time.Minute))

		pool := stdx509.NewCertPool()
		Expect(pool.AppendCertsFromPEM([]byte(cr.Status.Trust))).To(BeTrue())
		_, err = crt.Verify(stdx509.VerifyOptions{Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}})
		Expect(err).NotTo(HaveOccurred(), "the certificate must verify against the CA in status.trust")
	})

	It("gives each download purpose the least Talos role and the operator credentials the admin ones", func() {
		for purpose, org := range map[ichorv1alpha1.TalosCredentialPurpose]string{
			ichorv1alpha1.CredentialPurposeEtcdSnapshot:     "os:etcd:backup",
			ichorv1alpha1.CredentialPurposePacketCapture:    "os:operator",
			ichorv1alpha1.CredentialPurposeCredentialsTalos: "os:admin",
			ichorv1alpha1.CredentialPurposeSupportBundle:    "os:reader",
		} {
			csr, _, err := credentials.NewCSR(false)
			Expect(err).NotTo(HaveOccurred())

			spec := ichorv1alpha1.TalosCredentialRequestSpec{Purpose: purpose, CSR: string(csr)}
			if purpose != ichorv1alpha1.CredentialPurposeCredentialsTalos {
				spec.MachineRef = &corev1.LocalObjectReference{Name: "m"}
			}

			cr := request(spec)
			Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued), string(purpose)+": "+cr.Status.Message)
			Expect(parse(cr.Status.Certificate).Subject.Organization).To(ConsistOf(org), string(purpose))
		}
	})

	It("signs an os:reader certificate for the talosconfig a person takes, one hour by default and twelve at most", func() {
		csr, _, err := credentials.NewCSR(false)
		Expect(err).NotTo(HaveOccurred())

		// talosRole must not widen this purpose.
		cr := request(ichorv1alpha1.TalosCredentialRequestSpec{
			Purpose: ichorv1alpha1.CredentialPurposeTalosconfigReader, CSR: string(csr), TalosRole: "admin",
		})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued), cr.Status.Message)

		crt := parse(cr.Status.Certificate)
		Expect(crt.Subject.Organization).To(ConsistOf("os:reader"))
		Expect(crt.NotAfter).To(BeTemporally("~", time.Now().Add(credentials.TalosconfigLifetime), time.Minute))

		pool := stdx509.NewCertPool()
		Expect(pool.AppendCertsFromPEM([]byte(cr.Status.Trust))).To(BeTrue())
		_, err = crt.Verify(stdx509.VerifyOptions{Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}})
		Expect(err).NotTo(HaveOccurred())

		cr = request(ichorv1alpha1.TalosCredentialRequestSpec{
			Purpose: ichorv1alpha1.CredentialPurposeTalosconfigReader, CSR: string(csr),
			TTL: &metav1.Duration{Duration: credentials.MaxTalosconfigLifetime},
		})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued), cr.Status.Message)
		Expect(parse(cr.Status.Certificate).NotAfter).To(BeTemporally("~", time.Now().Add(credentials.MaxTalosconfigLifetime), time.Minute))

		cr = request(ichorv1alpha1.TalosCredentialRequestSpec{
			Purpose: ichorv1alpha1.CredentialPurposeTalosconfigReader, CSR: string(csr),
			TTL: &metav1.Duration{Duration: credentials.MaxTalosconfigLifetime + time.Hour},
		})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseFailed))
		Expect(cr.Status.Certificate).To(BeEmpty())
	})

	It("signs a Kubernetes administrator certificate for the credentials download", func() {
		csr, _, err := credentials.NewCSR(true)
		Expect(err).NotTo(HaveOccurred())

		cr := request(ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeCredentialsKubernetes, CSR: string(csr)})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued), cr.Status.Message)

		crt := parse(cr.Status.Certificate)
		Expect(crt.Subject.Organization).To(ConsistOf("system:masters"))
		Expect(crt.Subject.CommonName).To(Equal("admin"))
		Expect(crt.NotAfter).To(BeTemporally("~", time.Now().Add(credentials.Lifetime), time.Minute))
	})

	It("refuses a lifetime above the purpose's cap, and a request with SANs", func() {
		csr, _, err := credentials.NewCSR(false)
		Expect(err).NotTo(HaveOccurred())

		cr := request(ichorv1alpha1.TalosCredentialRequestSpec{
			Purpose: ichorv1alpha1.CredentialPurposeNodeReader, CSR: string(csr), TTL: &metav1.Duration{Duration: time.Hour},
		})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseFailed))
		Expect(cr.Status.Certificate).To(BeEmpty())

		cr = request(ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeNodeReader, CSR: "not a csr"})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseFailed))
	})

	It("returns only public facts for purpose public, and never a key", func() {
		cr := request(ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposePublic})
		Expect(cr.Status.Phase).To(Equal(ichorv1alpha1.CredentialPhaseIssued))
		Expect(cr.Status.Public.ClusterID).NotTo(BeEmpty())
		Expect(cr.Status.Public.TalosCA).To(ContainSubstring("BEGIN CERTIFICATE"))
		Expect(cr.Status.Public.KubernetesCA).To(ContainSubstring("BEGIN CERTIFICATE"))

		raw, err := json.Marshal(cr)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring("PRIVATE KEY"))
	})
})
