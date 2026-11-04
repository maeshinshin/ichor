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

package render

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	authIchorURL    = "https://ichor.example.com"
	authDiscovery   = "https://10.96.12.34:8444/.well-known/openid-configuration"
	authExternalURL = "https://idp.example.org"
	authKind        = "kind: KubeAuthenticationConfig"
)

// testCAPEM is a throwaway self-signed CA certificate (public part only).
func testCAPEM(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func authCluster(contract string, auth *ichorv1alpha1.TalosClusterAuthentication) *ichorv1alpha1.TalosCluster {
	c := testCluster("true")
	c.Spec.VersionContract = contract
	c.Spec.Authentication = auth

	return c
}

func externalIssuer() ichorv1alpha1.TalosClusterExternalIssuer {
	return ichorv1alpha1.TalosClusterExternalIssuer{
		IssuerURL: authExternalURL, Audiences: []string{"ichor"},
		UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
	}
}

// TestAuthenticationRender checks ADR 0094 Decision 3: Ichor's issuer through discoveryURL and the
// CA from the trust ConfigMap, the external provider as a second jwt entry, and the anonymous block
// kept because the document's configuration is merge:replace.
func TestAuthenticationRender(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("node-a", roleControlPlane, nil)
	worker := testMachine("node-w", roleWorker, nil)
	ca := testCAPEM(t)
	sys := &SystemInputs{OIDCTrust: &OIDCTrust{DiscoveryURL: authDiscovery, CAPEM: ca}}
	both := &ichorv1alpha1.TalosClusterAuthentication{
		Ichor:    &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: authIchorURL},
		External: []ichorv1alpha1.TalosClusterExternalIssuer{externalIssuer()},
	}

	t.Run("both issuers render as two jwt entries with anonymous kept", func(t *testing.T) {
		out, _, err := Render(authCluster(versionContract, both), cp, bundle, nil, sys)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		doc := section(string(out), authKind)
		if doc == "" {
			t.Fatalf("no KubeAuthenticationConfig document:\n%s", out)
		}

		requireAll(t, doc,
			"url: "+authIchorURL, "discoveryURL: "+authDiscovery, "- ichor-kube", "claim: sub", "prefix: 'ichor:'",
			"url: "+authExternalURL, "claim: email", "prefix: 'oidc:'", "- ichor",
			"startsWith(''system:'')", "/livez", "/readyz", "/healthz", "enabled: true", "-----BEGIN CERTIFICATE-----")

		if strings.Index(doc, authIchorURL) > strings.Index(doc, authExternalURL) {
			t.Errorf("Ichor's issuer must come first:\n%s", doc)
		}
	})

	t.Run("a worker gets no document", func(t *testing.T) {
		out, _, err := Render(authCluster(versionContract, both), worker, bundle, nil, sys)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		requireNone(t, string(out), authKind)
	})

	t.Run("unset leaves Talos's default: no jwt entry", func(t *testing.T) {
		doc := section(mustRender(t, authCluster(versionContract, nil), cp, bundle, nil), authKind)
		requireNone(t, doc, "issuer")
	})

	t.Run("a contract below 1.14 is refused", func(t *testing.T) {
		_, _, err := Render(authCluster("v1.13", both), cp, bundle, nil, sys)
		if err == nil || !strings.Contains(err.Error(), "v1.14") {
			t.Fatalf("Render error = %v, want one naming v1.14", err)
		}
	})

	t.Run("Ichor enabled without the trust ConfigMap is refused", func(t *testing.T) {
		_, _, err := Render(authCluster(versionContract, both), cp, bundle, nil, nil)
		if err == nil || !strings.Contains(err.Error(), OIDCTrustConfigMap) {
			t.Fatalf("Render error = %v, want one naming %s", err, OIDCTrustConfigMap)
		}
	})

	t.Run("the apiserver's own validation rejects a duplicate issuer", func(t *testing.T) {
		dup := externalIssuer()
		dup.IssuerURL = authIchorURL

		_, err := AuthenticationConfiguration(&ichorv1alpha1.TalosClusterAuthentication{
			Ichor:    both.Ichor,
			External: []ichorv1alpha1.TalosClusterExternalIssuer{dup},
		}, sys.OIDCTrust)
		if err == nil || !strings.Contains(err.Error(), "Duplicate") {
			t.Fatalf("err = %v, want a Duplicate issuer error", err)
		}
	})

	t.Run("an http issuer is rejected before it reaches a node", func(t *testing.T) {
		bad := externalIssuer()
		bad.IssuerURL = "http://idp.example.org"

		if _, err := AuthenticationConfiguration(&ichorv1alpha1.TalosClusterAuthentication{
			External: []ichorv1alpha1.TalosClusterExternalIssuer{bad},
		}, nil); err == nil {
			t.Fatal("want an error for an http issuer")
		}
	})
}

// TestAuthenticationMultipleAudiences covers the hardware spike's crash loop (ADR 0094 amendment):
// Talos accepts any document, and kube-apiserver then refuses two audiences without
// audienceMatchPolicy. The render sets MatchAny, and the apiserver's own validation, which is
// mandatory before rendering, rejects the file when it is missing.
func TestAuthenticationMultipleAudiences(t *testing.T) {
	e := externalIssuer()
	e.Audiences = []string{"ichor", "ichor-second"}

	cfg, err := AuthenticationConfiguration(&ichorv1alpha1.TalosClusterAuthentication{
		External: []ichorv1alpha1.TalosClusterExternalIssuer{e},
	}, nil)
	if err != nil {
		t.Fatalf("AuthenticationConfiguration: %v", err)
	}

	if got := cfg.JWT[0].Issuer.AudienceMatchPolicy; got != "MatchAny" {
		t.Fatalf("audienceMatchPolicy = %q, want MatchAny", got)
	}

	// The same file without the policy is exactly what crash-looped the apiserver.
	cfg.JWT[0].Issuer.AudienceMatchPolicy = ""

	err = validateAuthenticationConfiguration(cfg)
	if err == nil || !strings.Contains(err.Error(), "audienceMatchPolicy must be MatchAny for multiple audiences") {
		t.Fatalf("validation error = %v, want the audienceMatchPolicy error", err)
	}
}
