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

// Package webhookcert mints a webhook serving certificate for a bare IP address, signed
// by an existing CA. It exists because cmd/main.go's own cert-controller
// (github.com/open-policy-agent/cert-controller, ADR 0006) cannot: its generateCert sets
// x509.Certificate.DNSNames only, never IPAddresses, and Go's TLS client verifies an IP
// dial target against IPAddresses, not DNSNames, so a cert from that library can never
// satisfy a "https://<pod IP>" webhook URL no matter how it's configured (confirmed by
// reading the vendored source, docs/adr/0007). That library also never writes its cert to
// disk itself -- real deployments populate CertDir by mounting the Secret it writes as a
// volume, which a one-off verification pod has no way to do.
//
// So this package mints its own leaf for the pod's IP, signed by the same CA the rotator
// already generated and stored in its Secret (ca.crt/ca.key), and the caller drops the
// result straight into the pod's CertDir; controller-runtime's own certwatcher picks it up
// from there. The CA itself is never regenerated -- only cert-controller's rotator is
// trusted to make one, this package only ever signs one more leaf with it.
package webhookcert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// leafValidity is deliberately short: this is a throwaway verification cert for one
// harness run, not a certificate anyone should still be trusting a year from now.
const leafValidity = 24 * time.Hour

// MintLeafForIP signs a new serving certificate for ip, usable only as that IP's SAN,
// using caCertPEM/caKeyPEM (PEM-encoded, PKCS1 RSA key -- the exact shape
// open-policy-agent/cert-controller's rotator writes to its Secret's ca.crt/ca.key).
// It returns the new leaf's cert and key, both PEM-encoded.
func MintLeafForIP(caCertPEM, caKeyPEM []byte, ip string) (certPEM, keyPEM []byte, err error) {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return nil, nil, fmt.Errorf("webhookcert: %q is not an IP address", ip)
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, nil, fmt.Errorf("webhookcert: no PEM block found in the CA certificate")
	}
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("webhookcert: parsing CA certificate: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, nil, fmt.Errorf("webhookcert: no PEM block found in the CA key")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("webhookcert: parsing CA key: %w", err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("webhookcert: generating leaf key: %w", err)
	}

	templ := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: ip},
		IPAddresses:  []net.IP{parsedIP},
		NotBefore:    time.Now().Add(-time.Hour), // clock skew margin
		NotAfter:     time.Now().Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, templ, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("webhookcert: signing leaf certificate: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})
	return certPEM, keyPEM, nil
}
