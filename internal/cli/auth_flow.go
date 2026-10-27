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

package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	authExpirySkew   = 30 * time.Second
	authLoginTimeout = 5 * time.Minute
	authCallbackPath = "/callback"
)

// authFlow is the OIDC client half: authorization code
// + PKCE (S256) over a 127.0.0.1 loopback redirect (RFC 8252), refresh from
// the cache, never a password.
type authFlow struct {
	Issuer   string
	ClientID string
	Audience string
	Dir      string

	Now          func() time.Time
	HTTP         *http.Client      // nil: http.DefaultClient
	OpenBrowser  func(string) error // nil: only print the URL
	Stderr       io.Writer
	LoginTimeout time.Duration
}

func (f *authFlow) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}

	return time.Now()
}

func (f *authFlow) key() string { return authCacheKey(f.Issuer, f.ClientID, f.Audience) }

func (f *authFlow) logf(format string, args ...any) {
	if f.Stderr != nil {
		fmt.Fprintf(f.Stderr, format+"\n", args...)
	}
}

func (f *authFlow) withClient(ctx context.Context) context.Context {
	if f.HTTP != nil {
		return oidc.ClientContext(ctx, f.HTTP)
	}

	return ctx
}

type authProvider struct {
	cfg      *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func (f *authFlow) discover(ctx context.Context, redirect string) (*authProvider, error) {
	p, err := oidc.NewProvider(ctx, f.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering issuer %s: %w", f.Issuer, err)
	}

	ep := p.Endpoint()
	ep.AuthStyle = oauth2.AuthStyleInParams // public client: no secret, no Basic header

	return &authProvider{
		cfg: &oauth2.Config{
			ClientID:    f.ClientID,
			Endpoint:    ep,
			RedirectURL: redirect,
			Scopes:      []string{oidc.ScopeOpenID, oidc.ScopeOfflineAccess},
		},
		// The audience is checked below, since it may differ from the client id.
		verifier: p.VerifierContext(ctx, &oidc.Config{SkipClientIDCheck: true}),
	}, nil
}

// entryFrom verifies the ID token in tok (signature, iss, exp; nonce when one
// was sent; aud = the requested audience or the client id).
func (f *authFlow) entryFrom(ctx context.Context, p *authProvider, tok *oauth2.Token, nonce string) (*authCacheEntry, error) {
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("the token response has no id_token")
	}

	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verifying the ID token: %w", err)
	}

	if nonce != "" && subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("the ID token nonce does not match the login request")
	}

	want := f.Audience
	if want == "" {
		want = f.ClientID
	}

	if !slices.Contains(idt.Audience, want) {
		return nil, fmt.Errorf("the ID token audience %v does not include %q", idt.Audience, want)
	}

	return &authCacheEntry{
		Issuer: f.Issuer, ClientID: f.ClientID, Audience: f.Audience,
		IDToken: raw, Expiry: idt.Expiry, RefreshToken: tok.RefreshToken,
	}, nil
}

func (e *authCacheEntry) fresh(now time.Time) bool {
	return e.IDToken != "" && e.Expiry.Sub(now) > authExpirySkew
}

// refresh trades the cached refresh token for a new ID token.
func (f *authFlow) refresh(ctx context.Context, old *authCacheEntry) (*authCacheEntry, error) {
	p, err := f.discover(ctx, "")
	if err != nil {
		return nil, err
	}

	// A token with no access token and no expiry is always stale, so the
	// TokenSource goes straight to the refresh grant; it keeps the old refresh
	// token when the response omits one (the issuer's tokens do not rotate).
	tok, err := p.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: old.RefreshToken}).Token()
	if err != nil {
		return nil, fmt.Errorf("refreshing the login: %w", err)
	}

	return f.entryFrom(ctx, p, tok, "")
}

// ensure returns a usable entry: cached, refreshed, or (when interactive)
// freshly logged in. It holds the cache lock throughout.
func (f *authFlow) ensure(ctx context.Context, interactive bool) (*authCacheEntry, error) {
	ctx = f.withClient(ctx)

	unlock, err := lockAuthCache(f.Dir, f.key())
	if err != nil {
		return nil, err
	}
	defer unlock()

	e, err := loadAuthCache(f.Dir, f.key())
	if err != nil {
		return nil, err
	}

	if e != nil && e.fresh(f.now()) {
		return e, nil
	}

	if e != nil && e.RefreshToken != "" {
		n, rerr := f.refresh(ctx, e)
		if rerr == nil {
			return n, saveAuthCache(f.Dir, f.key(), n)
		}

		f.logf("cached login could not be refreshed: %v", rerr)
	}

	if !interactive {
		return nil, fmt.Errorf("no valid login for %s: run `ichor auth login --issuer %s --client-id %s`", f.Issuer, f.Issuer, f.ClientID)
	}

	n, err := f.login(ctx)
	if err != nil {
		return nil, err
	}

	return n, saveAuthCache(f.Dir, f.key(), n)
}

// loginAndSave is `ichor auth login`: always a fresh interactive login.
func (f *authFlow) loginAndSave(ctx context.Context) (*authCacheEntry, error) {
	ctx = f.withClient(ctx)

	unlock, err := lockAuthCache(f.Dir, f.key())
	if err != nil {
		return nil, err
	}
	defer unlock()

	n, err := f.login(ctx)
	if err != nil {
		return nil, err
	}

	return n, saveAuthCache(f.Dir, f.key(), n)
}

func randomURLString() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// login runs the authorization code flow with PKCE on a random loopback port.
func (f *authFlow) login(ctx context.Context) (*authCacheEntry, error) {
	lis, err := net.Listen("tcp", "127.0.0.1:0") // the literal, not "localhost" (RFC 8252 section 8.3)
	if err != nil {
		return nil, fmt.Errorf("listening for the login redirect: %w", err)
	}
	defer lis.Close()

	redirect := fmt.Sprintf("http://%s%s", lis.Addr().String(), authCallbackPath)

	p, err := f.discover(ctx, redirect)
	if err != nil {
		return nil, err
	}

	state, err := randomURLString()
	if err != nil {
		return nil, err
	}

	nonce, err := randomURLString()
	if err != nil {
		return nil, err
	}

	verifier := oauth2.GenerateVerifier()
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)}

	if f.Audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", f.Audience))
	}

	authURL := p.cfg.AuthCodeURL(state, opts...)

	results := make(chan authCallback, 1)
	srv := &http.Server{Handler: authCallbackHandler(state, results), ReadHeaderTimeout: 10 * time.Second}

	go func() { _ = srv.Serve(lis) }()

	defer func() { _ = srv.Close() }()

	f.logf("Open this URL in a browser to log in:\n  %s", authURL)

	if f.OpenBrowser != nil {
		if err := f.OpenBrowser(authURL); err != nil {
			f.logf("could not open a browser (%v); open the URL above yourself", err)
		}
	}

	timeout := f.LoginTimeout
	if timeout == 0 {
		timeout = authLoginTimeout
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var res authCallback

	select {
	case res = <-results:
	case <-timer.C:
		return nil, errors.New("timed out waiting for the browser login")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if res.err != nil {
		return nil, res.err
	}

	tok, err := p.cfg.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("exchanging the authorization code: %w", err)
	}

	return f.entryFrom(ctx, p, tok, nonce)
}

type authCallback struct {
	code string
	err  error
}

// authCallbackHandler accepts exactly one redirect carrying the expected
// state. A request with a different state (another local page poking the
// port) gets a 400 and is ignored, so it cannot abort or hijack the login.
func authCallbackHandler(state string, ch chan<- authCallback) http.Handler {
	var once sync.Once

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != authCallbackPath {
			http.NotFound(w, r)

			return
		}

		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "unexpected state", http.StatusBadRequest)

			return
		}

		var res authCallback

		switch {
		case q.Get("error") != "":
			res.err = fmt.Errorf("the issuer refused the login: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("code") == "":
			res.err = errors.New("the redirect carried no authorization code")
		default:
			res.code = q.Get("code")
		}

		once.Do(func() { ch <- res })

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, "Login failed; see the terminal.")

			return
		}

		fmt.Fprintln(w, "Logged in. You can close this tab and return to the terminal.")
	})
}
