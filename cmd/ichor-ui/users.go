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
	"bytes"
	"context"
	"crypto"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"

	"github.com/maeshinshin/ichor/internal/certreload"
	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/internal/uiapi"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

// Ichor's own users and issuer (docs/adr/0094). Everything here is off unless
// --users-enabled is set.

// uiClientID is the public client of the SPA and the audience of the token
// ichor-ui itself accepts.
const uiClientID = ichorauth.ClientUI

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
	signingKeysDir string
	// internalJWKSURI overrides the jwks_uri of the internal discovery document (default: the Service ClusterIP).
	internalJWKSURI      string
	tokenTTL, refreshTTL time.Duration
	// kubeAPIServerURL and kubeAPITLSServerName shape the kubeconfig GetMyKubeconfig returns.
	kubeAPIServerURL, kubeAPITLSServerName string
	// trustedProxies is a comma-separated list of CIDRs whose X-Forwarded-For the login's
	// per-source limits believe (docs/adr/0108); empty means the TCP peer is the client.
	trustedProxies string
}

// proxyPrefixes parses --trusted-proxy-cidrs.
func (f usersFlags) proxyPrefixes() ([]netip.Prefix, error) {
	var out []netip.Prefix

	for _, c := range strings.Split(f.trustedProxies, ",") {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}

		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("--trusted-proxy-cidrs: %w", err)
		}

		out = append(out, p)
	}

	return out, nil
}

func (f usersFlags) validate() error {
	if !f.enabled {
		return nil
	}

	isHTTPS := func(s string) bool {
		u, err := url.Parse(s)

		return err == nil && u.Scheme == "https" && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
	}

	if !isHTTPS(f.externalURL) {
		return fmt.Errorf("--external-url must be an https URL, got %q", f.externalURL)
	}

	if f.kubeAPIServerURL != "" && !isHTTPS(f.kubeAPIServerURL) {
		return fmt.Errorf("--kube-api-server-url must be an https URL, got %q", f.kubeAPIServerURL)
	}

	if _, err := f.proxyPrefixes(); err != nil {
		return err
	}

	if f.tokenTTL < ichorauth.MinTokenTTL || f.tokenTTL > ichorauth.MaxTokenTTL {
		return errors.New("--token-ttl must be 1 minute to 1 hour")
	}

	if f.refreshTTL < ichorauth.MinRefreshLifetime || f.refreshTTL > ichorauth.MaxRefreshLifetime {
		return errors.New("--refresh-lifetime must be 1 hour to 30 days")
	}

	return nil
}

// localVerifier verifies an ID token of Ichor's own issuer for audience
// ichor-ui, in process, against the issuer's currently published keys.
type localVerifier struct {
	keys   interface{ Public() []jose.JSONWebKey }
	issuer string
}

func (v localVerifier) Verify(ctx context.Context, raw string) (uiapi.TokenInfo, error) {
	pub := make([]crypto.PublicKey, 0, len(v.keys.Public()))

	for _, k := range v.keys.Public() {
		pub = append(pub, k.Public().Key)
	}

	if len(pub) == 0 {
		return uiapi.TokenInfo{}, errors.New("no signing keys")
	}

	tok, err := oidc.NewVerifier(v.issuer, &oidc.StaticKeySet{PublicKeys: pub}, &oidc.Config{
		ClientID: ichorauth.AudienceUI, SupportedSigningAlgs: []string{ichorauth.SigningAlgorithm},
	}).Verify(ctx, raw)
	if err != nil {
		return uiapi.TokenInfo{}, err
	}

	return uiapi.TokenInfo{Subject: tok.Subject, IssuedAt: tok.IssuedAt, Expires: tok.Expiry}, nil
}

// keysFile is the key list inside the mounted ichor-oidc-signing Secret.
const keysFile = "keys.json"

// watchKeys loads keys.json now and again whenever it changes. The kubelet
// swaps the mounted Secret in place, so it polls; a read or parse failure
// keeps the previous keys and is logged without the content.
func watchKeys(ctx context.Context, store *oidcissuer.KeyStore, dir string) {
	var last []byte

	load := func() {
		data, err := os.ReadFile(filepath.Join(dir, keysFile))
		if err != nil || bytes.Equal(data, last) {
			return
		}

		if err := store.Update(data); err != nil {
			log.Printf("ichor-ui: signing keys not loaded: %v", err)

			return
		}

		last = data
	}

	load()

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				load()
			}
		}
	}()
}

// newIssuer builds Ichor's own issuer and the verifier for its ichor-ui tokens.
func newIssuer(ctx context.Context, f usersFlags, live *settings.Reader, backend *uiapi.IssuerBackend) (*oidcissuer.Issuer, localVerifier, error) {
	keys := oidcissuer.NewKeyStore(nil)
	watchKeys(ctx, keys, f.signingKeysDir)

	proxies, _ := f.proxyPrefixes() // validated at startup

	iss, err := oidcissuer.New(oidcissuer.Config{
		TrustedProxies: proxies,
		Issuer:         f.externalURL, InternalJWKSURI: f.internalJWKSURI, InternalJWKSFromHost: true, UIRedirectURI: f.externalURL + "/auth/callback",
		TokenTTL: f.tokenTTL, RefreshLifetime: f.refreshTTL,
		Lifetimes: func(ctx context.Context) (time.Duration, time.Duration) {
			e, _ := live.Effective(ctx)

			return e.TokenTTL.V, e.RefreshLifetime.V
		},
		Keys: keys, Users: backend, Passwords: backend, Refresh: backend,
	})

	return iss, localVerifier{keys: keys, issuer: f.externalURL}, err
}

// mountIssuer serves the issuer's external endpoints on mux.
func mountIssuer(mux *http.ServeMux, iss *oidcissuer.Issuer) {
	h := iss.Handler()
	mux.Handle("/.well-known/openid-configuration", h)
	mux.Handle("/oidc/", h)
}

// internalServer is the HTTPS listener for the kube-apiserver: discovery and
// JWKS only. The certificate files are watched, so a leaf the controller or
// cert-manager renews is picked up without a restart; a bad new pair keeps the old one.
func internalServer(ctx context.Context, f usersFlags, iss *oidcissuer.Issuer) *http.Server {
	mux := http.NewServeMux()
	h := iss.InternalHandler()
	mux.Handle("/.well-known/openid-configuration", h)
	mux.Handle("/oidc/jwks", h)

	return newInternalHTTPServer(f.internalAddr, mux, certreload.GetCertificate(ctx, filepath.Join(f.internalTLSDir, "tls.crt"), filepath.Join(f.internalTLSDir, "tls.key"), certreload.Interval))
}

// authProvider is one sign-in choice in /auth/config.
type authProvider struct {
	ID       string `json:"id"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"clientId"`
}

// authConfig builds the /auth/config answer. providers lists every sign-in
// choice; enabled stays top-level because the separately-versioned CLI reads it. With no
// provider, anonymous says what the server does about it: "readonly" serves reads only, "open"
// is the explicit --allow-unauthenticated mode (docs/adr/0108); the UI shows why.
func authConfig(oidcIssuer, oidcClientID string, users usersFlags, allowAnonymous bool) map[string]any {
	providers := []authProvider{}
	if oidcIssuer != "" {
		providers = append(providers, authProvider{ID: uiapi.SourceOIDC, Issuer: oidcIssuer, ClientID: oidcClientID})
	}

	if users.enabled {
		providers = append(providers, authProvider{ID: uiapi.SourceIchor, Issuer: users.externalURL, ClientID: uiClientID})
	}

	out := map[string]any{"enabled": len(providers) > 0, "providers": providers}

	switch {
	case len(providers) > 0:
	case allowAnonymous:
		out["anonymous"] = "open"
	default:
		out["anonymous"] = "readonly"
	}

	return out
}
