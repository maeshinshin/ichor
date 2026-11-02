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
	"crypto/ed25519"
	"crypto/rand"
	stdx509 "crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestPurposeLifetime(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		purpose   ichorv1alpha1.TalosCredentialPurpose
		asked     time.Duration
		want      time.Duration
		wantError bool
	}{
		{ichorv1alpha1.CredentialPurposeNodeReader, 0, DialLifetime, false},
		{ichorv1alpha1.CredentialPurposeEtcdSnapshot, 15 * time.Minute, 15 * time.Minute, false},
		{ichorv1alpha1.CredentialPurposeEtcdSnapshot, time.Hour, 0, true},
		{ichorv1alpha1.CredentialPurposeCredentialsTalos, 0, Lifetime, false},
		{ichorv1alpha1.CredentialPurposeCredentialsKubernetes, 24 * time.Hour, 24 * time.Hour, false},
		{ichorv1alpha1.CredentialPurposeCredentialsKubernetes, 2 * Lifetime, 0, true},
		{ichorv1alpha1.CredentialPurposeCredentialsKubernetes, -time.Second, 0, true},
	} {
		got, err := PurposeLifetime(tt.purpose, tt.asked)
		if (err != nil) != tt.wantError || got != tt.want {
			t.Errorf("%s asked %s: got %s, %v; want %s, error=%v", tt.purpose, tt.asked, got, err, tt.want, tt.wantError)
		}
	}
}

// The identity of a certificate is decided by the signer, never by the request.
func TestIssueIgnoresTheRequestsSubjectAndRefusesSANs(t *testing.T) {
	t.Parallel()

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), nil)
	if err != nil {
		t.Fatal(err)
	}

	src, err := NewSources(bundle, nil)
	if err != nil {
		t.Fatal(err)
	}

	csr, _, err := NewCSR(false)
	if err != nil {
		t.Fatal(err)
	}

	crtPEM, notAfter, err := src.IssueTalos(csr, role.MakeSet(role.Reader), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	block, _ := pem.Decode(crtPEM)

	crt, err := stdx509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	if len(crt.Subject.Organization) != 1 || crt.Subject.Organization[0] != "os:reader" || crt.Subject.CommonName != "" {
		t.Fatalf("subject = %v", crt.Subject)
	}

	if crt.NotAfter.After(notAfter.Add(time.Second)) || crt.NotAfter.Before(time.Now()) {
		t.Fatalf("notAfter = %s", crt.NotAfter)
	}

	// A request that names a SAN and an organization of its own.
	_, key, _ := ed25519.GenerateKey(rand.Reader)

	der, err := stdx509.CreateCertificateRequest(rand.Reader, &stdx509.CertificateRequest{DNSNames: []string{"evil.example"}}, key)
	if err != nil {
		t.Fatal(err)
	}

	bad := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if _, _, err := src.IssueTalos(bad, role.MakeSet(role.Reader), time.Minute); err == nil {
		t.Fatal("a request with a SAN was signed")
	}

	if _, _, err := src.IssueTalos([]byte("junk"), role.MakeSet(role.Reader), time.Minute); err == nil {
		t.Fatal("junk was signed")
	}
}
