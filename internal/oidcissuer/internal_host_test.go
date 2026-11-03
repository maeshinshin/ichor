package oidcissuer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

// A recreated Service gets a new ClusterIP; the internal discovery document names the host the
// caller used, so it follows without a change to ichor-ui.
func TestInternalDiscoveryFollowsHost(t *testing.T) {
	e := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.InternalJWKSFromHost = true })

	for _, host := range []string{"10.96.0.7:8444", "10.96.0.9:8444"} {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/.well-known/openid-configuration", nil)
		rec := httptest.NewRecorder()
		e.iss.InternalHandler().ServeHTTP(rec, req)

		var doc map[string]any
		if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["jwks_uri"] != "https://"+host+"/oidc/jwks" {
			t.Fatalf("host %s: jwks_uri %v", host, doc["jwks_uri"])
		}
	}
}
