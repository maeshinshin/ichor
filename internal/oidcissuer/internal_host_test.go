package oidcissuer_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

// A recreated Service gets a new ClusterIP; the internal discovery document names the host the
// caller used, so it follows without a change to ichor-ui.
func TestInternalDiscoveryFollowsHost(t *testing.T) {
	e := newEnv(t, externalURL, "")

	iss, err := oidcissuer.New(oidcissuer.Config{
		Issuer: externalURL, InternalJWKSFromHost: true, UIRedirectURI: uiRedirect,
		Keys: e.keys, Users: e.dir, Passwords: e.dir, Now: e.clk.now,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, host := range []string{"10.96.0.7:8444", "10.96.0.9:8444"} {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/.well-known/openid-configuration", nil)
		rec := httptest.NewRecorder()
		iss.InternalHandler().ServeHTTP(rec, req)

		var doc map[string]any
		if jsonUnmarshal(rec.Body.Bytes(), &doc) != nil || doc["jwks_uri"] != "https://"+host+"/oidc/jwks" {
			t.Fatalf("host %s: jwks_uri %v", host, doc["jwks_uri"])
		}
	}
}
