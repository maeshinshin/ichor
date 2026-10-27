package oidcissuer_test

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	k8soidc "k8s.io/apiserver/plugin/pkg/authenticator/token/oidc"
	"k8s.io/utils/ptr"
)

// These tests run the real consumers: go-oidc (what ichor-ui uses for external
// IdPs and what the apiserver's authenticator is built on) and the
// k8s.io/apiserver OIDC authenticator itself (the code kube-apiserver runs).

func TestGoOIDCVerifiesIDToken(t *testing.T) {
	srv, serve := startTLS(t)
	e := newEnv(t, srv.URL, "")
	serve(e.h)
	ctx := oidc.ClientContext(context.Background(), srv.Client())
	prov, err := oidc.NewProvider(ctx, srv.URL)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	if prov.Endpoint().TokenURL != srv.URL+"/oidc/token" || prov.Endpoint().AuthURL != srv.URL+"/oidc/authorize" {
		t.Fatalf("endpoints %+v", prov.Endpoint())
	}

	tr := e.tokens("ichor-ui", uiRedirect, "")
	idt, err := prov.Verifier(&oidc.Config{ClientID: "ichor-ui"}).Verify(ctx, tr.IDToken)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var c struct {
		Groups []string `json:"groups"`
		Nonce  string   `json:"nonce"`
	}
	if err := idt.Claims(&c); err != nil {
		t.Fatal(err)
	}
	if idt.Subject != "alice" || idt.Issuer != srv.URL || c.Nonce != "n1" || !slices.Equal(c.Groups, []string{"admin"}) {
		t.Fatalf("claims: sub=%q iss=%q %+v", idt.Subject, idt.Issuer, c)
	}
	if got := idt.Expiry.Sub(idt.IssuedAt); got != 5*time.Minute {
		t.Fatalf("ttl = %s", got)
	}
	if tr.ExpiresIn != 300 || tr.TokenType != "Bearer" {
		t.Fatalf("response %+v", tr)
	}
	// The browser token is not a Kubernetes credential: wrong audience fails.
	if _, err := prov.Verifier(&oidc.Config{ClientID: "ichor-kube"}).Verify(ctx, tr.IDToken); err == nil {
		t.Fatal("ichor-ui token verified for audience ichor-kube")
	}
	// Internal tokens are no ID tokens.
	if _, err := prov.Verifier(&oidc.Config{ClientID: "ichor-ui"}).Verify(ctx, tr.RefreshToken); err == nil {
		t.Fatal("refresh token verified as ID token")
	}
}

func caProvider(t *testing.T, cert *x509.Certificate) k8soidc.CAContentProvider {
	t.Helper()
	return staticCA(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

type staticCA []byte

func (s staticCA) CurrentCABundleContent() []byte { return s }

// apiserverAuthenticator builds the real authenticator for issuer URL
// externalURL reached through the internal discoveryURL on srv.
func apiserverAuthenticator(t *testing.T, srvCert *x509.Certificate, discovery string) authenticator.Token {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := k8soidc.New(ctx, k8soidc.Options{
		JWTAuthenticator: apiserver.JWTAuthenticator{
			Issuer: apiserver.Issuer{
				URL: externalURL, DiscoveryURL: discovery, Audiences: []string{"ichor-kube"},
			},
			ClaimMappings: apiserver.ClaimMappings{
				Username: apiserver.PrefixedClaimOrExpression{Claim: "sub", Prefix: ptr.To("ichor:")},
				Groups:   apiserver.PrefixedClaimOrExpression{Claim: "groups", Prefix: ptr.To("ichor:")},
			},
		},
		CAContentProvider: caProvider(t, srvCert),
		APIServerID:       "test",
	})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return a
}

// authOK polls until the asynchronously initialised authenticator answers.
func authOK(t *testing.T, a authenticator.Token, token string) *authenticator.Response {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, ok, err := a.AuthenticateToken(context.Background(), token)
		if ok {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("authenticator never accepted the token: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func authRejected(t *testing.T, a authenticator.Token, name, token string) {
	t.Helper()
	if _, ok, _ := a.AuthenticateToken(context.Background(), token); ok {
		t.Fatalf("%s: token accepted by the apiserver authenticator", name)
	}
}

func TestAPIServerAuthenticator(t *testing.T) {
	// The internal listener: discovery says issuer = external URL, jwks_uri =
	// internal address. The apiserver is pointed at it through discoveryURL.
	srv, serve := startTLS(t)
	e := newEnv(t, externalURL, srv.URL+"/oidc/jwks")
	serve(e.iss.InternalHandler())
	a := apiserverAuthenticator(t, srv.Certificate(), srv.URL+"/.well-known/openid-configuration")

	good := e.tokens("ichor-cli", cliRedirect, "ichor-kube")
	resp := authOK(t, a, good.IDToken)
	if resp.User.GetName() != "ichor:alice" || !slices.Contains(resp.User.GetGroups(), "ichor:admin") {
		t.Fatalf("user = %q groups = %v", resp.User.GetName(), resp.User.GetGroups())
	}
	// The CLI default audience is the Kubernetes one.
	if _, tr := e.token(refreshForm("ichor-cli", good.RefreshToken, "")); authOK(t, a, tr.IDToken).User.GetName() != "ichor:alice" {
		t.Fatal("refreshed token rejected")
	}

	t.Run("browser audience", func(t *testing.T) {
		ui := e.tokens("ichor-ui", uiRedirect, "")
		authRejected(t, a, "ichor-ui id token", ui.IDToken)
		_, viaRefresh := e.token(refreshForm("ichor-cli", good.RefreshToken, "ichor-ui"))
		authRejected(t, a, "cli token with ichor-ui audience", viaRefresh.IDToken)
	})
	t.Run("internal tokens", func(t *testing.T) {
		authRejected(t, a, "refresh token", good.RefreshToken)
	})
	t.Run("tampered", func(t *testing.T) {
		parts := strings.Split(good.IDToken, ".")
		sig := []byte(parts[2])
		sig[10] ^= 1
		parts[2] = string(sig)
		authRejected(t, a, "bit-flipped signature", strings.Join(parts, "."))
		parts = strings.Split(good.IDToken, ".")
		parts[1] = strings.Replace(parts[1], parts[1][5:6], "A", 1)
		authRejected(t, a, "altered payload", strings.Join(parts, "."))
	})
	t.Run("expired", func(t *testing.T) {
		e.clk.shift(-time.Hour)
		old := e.tokens("ichor-cli", cliRedirect, "ichor-kube")
		e.clk.shift(time.Hour)
		authRejected(t, a, "expired", old.IDToken)
	})
	t.Run("key rotation without restart", func(t *testing.T) {
		before := claimsKID(t, good.IDToken)
		k2 := keyRecord(t, "k2", time.Now().Add(-time.Minute))
		if err := e.keys.Update(keysJSON(t, e.recs[0], k2)); err != nil {
			t.Fatal(err)
		}
		rot := e.tokens("ichor-cli", cliRedirect, "ichor-kube")
		if claimsKID(t, rot.IDToken) != "k2" || before == "k2" {
			t.Fatalf("kid before %q after %q", before, claimsKID(t, rot.IDToken))
		}
		authOK(t, a, rot.IDToken)
	})
}

func claimsKID(t *testing.T, jws string) string {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Split(jws, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		KID string `json:"kid"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	return h.KID
}
