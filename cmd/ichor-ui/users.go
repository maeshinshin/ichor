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

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// Ichor's own users and issuer. Everything here is off unless
// --users-enabled is set.

// uiClientID is the public client of the SPA and the audience of the token
// ichor-ui itself accepts.
const uiClientID = "ichor-ui"

// usersFlags are the flags that turn Ichor's own users on.
type usersFlags struct {
	enabled bool
	// externalURL is the https URL of ichor-ui as browsers and the CLI reach it, and the issuer (iss).
	externalURL string
	// internalAddr is the HTTPS listener for the kube-apiserver (discovery and JWKS only).
	internalAddr string
	// internalTLSDir holds tls.crt and tls.key of the internal listener (Secret ichor-oidc-tls).
	internalTLSDir string
	// signingKeysDir holds keys.json (Secret ichor-oidc-signing), written by the controller only.
	signingKeysDir       string
	tokenTTL, refreshTTL time.Duration
}

func (f usersFlags) validate() error {
	if !f.enabled {
		return nil
	}

	u, err := url.Parse(f.externalURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("--external-url must be an https URL, got %q", f.externalURL)
	}

	if f.tokenTTL < time.Minute || f.tokenTTL > 15*time.Minute {
		return errors.New("--token-ttl must be 1 to 15 minutes")
	}

	if f.refreshTTL < time.Hour || f.refreshTTL > 30*24*time.Hour {
		return errors.New("--refresh-lifetime must be 1 hour to 30 days")
	}

	return nil
}

// localVerifier adapts the issuer to uiapi.LocalVerifier.
type localVerifier struct{ iss *oidcissuer.Issuer }

func (v localVerifier) Verify(ctx context.Context, raw string) (string, error) {
	c, err := v.iss.Verify(ctx, raw)
	if err != nil {
		return "", err
	}

	return c.Subject, nil
}

// newIssuer builds Ichor's own issuer.
func newIssuer(f usersFlags) (*oidcissuer.Issuer, error) {
	return oidcissuer.New(oidcissuer.Config{
		Issuer: f.externalURL, Audience: uiClientID, KeysDir: f.signingKeysDir,
		TokenTTL: f.tokenTTL, RefreshTTL: f.refreshTTL,
	})
}

// mountIssuer serves the issuer's external endpoints on mux.
func mountIssuer(mux *http.ServeMux, iss *oidcissuer.Issuer) {
	h := iss.Handler()
	mux.Handle("/.well-known/openid-configuration", h)
	mux.Handle("/oidc/", h)
}

// internalServer is the HTTPS listener for the kube-apiserver: discovery and
// JWKS only. The certificate is read from disk on every handshake, so a leaf
// the controller renews is picked up without a restart.
func internalServer(f usersFlags, iss *oidcissuer.Issuer) *http.Server {
	mux := http.NewServeMux()
	h := iss.InternalHandler()
	mux.Handle("/.well-known/openid-configuration", h)
	mux.Handle("/oidc/jwks", h)

	return &http.Server{
		Addr: f.internalAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				c, err := tls.LoadX509KeyPair(filepath.Join(f.internalTLSDir, "tls.crt"), filepath.Join(f.internalTLSDir, "tls.key"))
				if err != nil {
					return nil, errors.New("the internal TLS certificate is not available yet")
				}

				return &c, nil
			},
		},
	}
}

// authProvider is one sign-in choice in /auth/config.
type authProvider struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"clientId"`
}

// authConfig builds the /auth/config answer. providers lists every sign-in
// choice; enabled, issuer and clientId keep their meaning (the
// external IdP, or the only provider) so an older CLI still sees "enabled".
func authConfig(oidcIssuer, oidcClientID string, users usersFlags) map[string]any {
	providers := []authProvider{}
	if oidcIssuer != "" {
		providers = append(providers, authProvider{ID: uiapi.SourceOIDC, Issuer: oidcIssuer, ClientID: oidcClientID})
	}

	if users.enabled {
		providers = append(providers, authProvider{ID: uiapi.SourceIchor, Issuer: users.externalURL, ClientID: uiClientID})
	}

	out := map[string]any{"enabled": len(providers) > 0, "issuer": "", "clientId": "", "providers": providers}
	if len(providers) > 0 {
		out["issuer"], out["clientId"] = providers[0].Issuer, providers[0].ClientID
	}

	return out
}
