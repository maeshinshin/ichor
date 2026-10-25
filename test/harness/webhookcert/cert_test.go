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

package webhookcert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// selfSignedCA mints a throwaway CA in the exact shape
// open-policy-agent/cert-controller's rotator writes to its Secret (PEM, PKCS1 RSA key),
// so this test exercises MintLeafForIP against the real input shape, not a stand-in.
func selfSignedCA(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	templ := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, templ, templ, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("self-signing CA: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

// TestMintLeafForIPVerifiesAgainstTheCA proves the whole point of this package: a client
// doing an ordinary Go TLS IP-address verification (crypto/x509.Certificate.VerifyHostname
// with a literal IP, exactly what dialling "https://<pod IP>" triggers) accepts the minted
// leaf when it trusts the CA that signed it -- and rejects both a different IP and an
// untrusted CA.
func TestMintLeafForIPVerifiesAgainstTheCA(t *testing.T) {
	caCertPEM, caKeyPEM := selfSignedCA(t)

	certPEM, keyPEM, err := MintLeafForIP(caCertPEM, caKeyPEM, "10.244.0.174")
	if err != nil {
		t.Fatalf("MintLeafForIP: %v", err)
	}
	if len(keyPEM) == 0 {
		t.Fatalf("MintLeafForIP returned no key")
	}

	leafBlock, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing minted leaf: %v", err)
	}

	caBlock, _ := pem.Decode(caCertPEM)
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing CA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)

	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		t.Errorf("leaf does not chain to the CA that signed it: %v", err)
	}
	if err := leaf.VerifyHostname("10.244.0.174"); err != nil {
		t.Errorf("leaf does not verify for its own IP SAN: %v", err)
	}
	if err := leaf.VerifyHostname("10.244.0.175"); err == nil {
		t.Error("leaf verified for an IP it was never minted for")
	}

	otherCACertPEM, _ := selfSignedCA(t)
	otherBlock, _ := pem.Decode(otherCACertPEM)
	otherCA, err := x509.ParseCertificate(otherBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing other CA: %v", err)
	}
	wrongRoots := x509.NewCertPool()
	wrongRoots.AddCert(otherCA)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: wrongRoots}); err == nil {
		t.Error("leaf chained to a CA that never signed it")
	}
}

// TestMintLeafForIPRejectsAHostname guards the one input error this package can catch
// early: a DNS name instead of an IP would silently produce a cert with no SAN at all
// (net.ParseIP returns nil, and an empty IPAddresses slice is not a visible failure until
// a client tries to use it), so this fails loudly instead.
func TestMintLeafForIPRejectsAHostname(t *testing.T) {
	caCertPEM, caKeyPEM := selfSignedCA(t)
	if _, _, err := MintLeafForIP(caCertPEM, caKeyPEM, "not-an-ip.example"); err == nil {
		t.Error("expected an error for a non-IP argument")
	}
}
