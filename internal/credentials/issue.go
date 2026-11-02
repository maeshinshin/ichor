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

package credentials

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	stdx509 "crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The controller signs certificates for keys the requester keeps: the requester makes a
// key pair and a PKCS#10 request (NewCSR), the controller checks the request and signs its public key
// (IssueTalos, IssueKubernetes). The private key never leaves the requester.

const (
	// DialLifetime is the default lifetime of a certificate made for one call, MaxDialLifetime its cap.
	DialLifetime    = 10 * time.Minute
	MaxDialLifetime = 15 * time.Minute

	// MaxLifetime caps a certificate handed to an operator; it is Lifetime, the long-standing default.
	MaxLifetime = Lifetime

	// issueSkew backdates a certificate's start so a node whose clock is a little behind accepts it.
	issueSkew = 5 * time.Minute
)

// IsDialPurpose reports whether purpose is a certificate for one call (short-lived), as opposed to an
// operator credential.
func IsDialPurpose(p ichorv1alpha1.TalosCredentialPurpose) bool {
	switch p {
	case ichorv1alpha1.CredentialPurposeNodeReader, ichorv1alpha1.CredentialPurposeSupportBundle,
		ichorv1alpha1.CredentialPurposeEtcdSnapshot, ichorv1alpha1.CredentialPurposePacketCapture:
		return true
	default:
		return false
	}
}

// PurposeLifetime is the lifetime of a certificate for purpose: requested, or the default when zero,
// and an error above the purpose's cap.
func PurposeLifetime(p ichorv1alpha1.TalosCredentialPurpose, requested time.Duration) (time.Duration, error) {
	def, limit := MaxLifetime, MaxLifetime

	if IsDialPurpose(p) {
		def, limit = DialLifetime, MaxDialLifetime
	}

	switch {
	case requested == 0:
		return def, nil
	case requested < 0:
		return 0, errors.New("the lifetime is negative")
	case requested > limit:
		return 0, fmt.Errorf("a %s certificate lives at most %s, %s was asked for", p, limit, requested)
	default:
		return requested, nil
	}
}

// TalosRoles is the Talos role set of purpose: the least role the call needs. talosRole is only read for
// credentials-talos ("reader" or "admin", empty is admin).
func TalosRoles(p ichorv1alpha1.TalosCredentialPurpose, talosRole string) (role.Set, error) {
	switch p {
	case ichorv1alpha1.CredentialPurposeNodeReader, ichorv1alpha1.CredentialPurposeSupportBundle:
		return role.MakeSet(role.Reader), nil
	case ichorv1alpha1.CredentialPurposeEtcdSnapshot:
		return role.MakeSet(role.EtcdBackup), nil
	case ichorv1alpha1.CredentialPurposePacketCapture:
		return role.MakeSet(role.Operator), nil
	case ichorv1alpha1.CredentialPurposeCredentialsTalos:
		if talosRole == "reader" {
			return role.MakeSet(role.Reader), nil
		}

		return role.MakeSet(role.Admin), nil
	default:
		return role.Set{}, fmt.Errorf("purpose %q has no Talos role", p)
	}
}

// NewCSR makes a key pair and a certificate request for it. The request's subject is empty: the signer
// decides the identity. kubernetes picks ECDSA P-256 (the widest client support), else Ed25519 (what Talos
// itself uses). The key is PKCS#8 PEM; keep it in memory and out of every log and error.
func NewCSR(kubernetes bool) (csrPEM, keyPEM []byte, err error) {
	var key crypto.Signer

	if kubernetes {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	} else {
		_, key, err = ed25519.GenerateKey(rand.Reader)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("generating a key: %w", err)
	}

	der, err := stdx509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding the key: %w", err)
	}

	csr, err := stdx509.CreateCertificateRequest(rand.Reader, &stdx509.CertificateRequest{}, key)
	if err != nil {
		return nil, nil, fmt.Errorf("making the certificate request: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// checkCSR refuses a request that is not a plain proof-of-possession of an acceptable key.
func checkCSR(csrPEM []byte) error {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return errors.New("spec.csr is not a PEM certificate request")
	}

	csr, err := stdx509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return fmt.Errorf("spec.csr: %w", err)
	}

	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("spec.csr: its signature does not verify: %w", err)
	}

	if len(csr.DNSNames)+len(csr.IPAddresses)+len(csr.EmailAddresses)+len(csr.URIs) > 0 {
		return errors.New("spec.csr names SANs; the identity is decided by the controller, not the request")
	}

	switch k := csr.PublicKey.(type) {
	case ed25519.PublicKey:
	case *ecdsa.PublicKey:
		if k.Curve.Params().BitSize < 256 {
			return errors.New("spec.csr: the key is weaker than P-256")
		}
	case *rsa.PublicKey:
		if k.N.BitLen() < 2048 {
			return errors.New("spec.csr: the RSA key is shorter than 2048 bits")
		}
	default:
		return errors.New("spec.csr: unsupported key type")
	}

	return nil
}

func sign(signer *x509.PEMEncodedCertificateAndKey, csrPEM []byte, ttl time.Duration, usage stdx509.KeyUsage, subject pkix.Name) ([]byte, time.Time, error) {
	if err := checkCSR(csrPEM); err != nil {
		return nil, time.Time{}, err
	}

	now := time.Now()
	notAfter := now.Add(ttl)

	crt, err := x509.NewCertificateFromCSRBytes(signer.Crt, signer.Key, csrPEM,
		x509.NotBefore(now.Add(-issueSkew)), x509.NotAfter(notAfter),
		x509.KeyUsage(usage), x509.ExtKeyUsage([]stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}),
		x509.OverrideSubject(func(n *pkix.Name) { *n = subject }),
	)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("signing: %w", err)
	}

	return crt.X509CertificatePEM, notAfter, nil
}

// IssueTalos signs csrPEM as a Talos API client with roles, valid for ttl, with the Talos signer (the new
// CA during a rotation).
func (s *Sources) IssueTalos(csrPEM []byte, roles role.Set, ttl time.Duration) (certPEM []byte, notAfter time.Time, err error) {
	if roles.Empty() {
		return nil, time.Time{}, errors.New("at least one Talos role is required")
	}

	return sign(s.TalosSigner, csrPEM, ttl, stdx509.KeyUsageDigitalSignature, pkix.Name{Organization: roles.Strings()})
}

// IssueKubernetes signs csrPEM as the Kubernetes cluster administrator, valid for ttl, with the Kubernetes
// signer (the new CA during a rotation).
func (s *Sources) IssueKubernetes(csrPEM []byte, ttl time.Duration) (certPEM []byte, notAfter time.Time, err error) {
	return sign(s.KubernetesSigner, csrPEM, ttl, stdx509.KeyUsageDigitalSignature|stdx509.KeyUsageKeyEncipherment,
		pkix.Name{CommonName: KubeAdminCommonName, Organization: []string{KubeAdminOrganization}})
}
