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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
	clientauthv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
)

const (
	fakeRefreshToken = "refresh-secret-1"
	execInfoV1       = `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":%t}}`
)

// fakeIssuer is the smallest OIDC issuer that checks what the CLI must get
// right: PKCE S256, the redirect URI, state and nonce echo.
type fakeIssuer struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu           sync.Mutex
	codes        map[string]fakeCode
	authorizes   []url.Values
	refreshes    int
	failRefresh  bool
	wrongNonce   bool
	idTokenTTL   time.Duration
	lastAudience string
	ignoreAud    bool
	// RFC 7009 sign-out: what /revoke received, whether discovery advertises it, and what it answers (0 is 200).
	revoked      []url.Values
	noRevocation bool
	revokeStatus int
}

type fakeCode struct {
	challenge, nonce, redirect string
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeIssuer{t: t, key: key, codes: map[string]fakeCode{}, idTokenTTL: 5 * time.Minute}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("/keys", f.keys)
	mux.HandleFunc("/authorize", f.authorize)
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", f.revoke)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeIssuer) discovery(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		"issuer":                                f.srv.URL,
		"authorization_endpoint":                f.srv.URL + "/authorize",
		"token_endpoint":                        f.srv.URL + "/token",
		"jwks_uri":                              f.srv.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}

	if !f.noRevocation {
		doc["revocation_endpoint"] = f.srv.URL + "/revoke"
	}

	_ = json.NewEncoder(w).Encode(doc)
}

func (f *fakeIssuer) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	f.mu.Lock()
	defer f.mu.Unlock()

	f.revoked = append(f.revoked, r.PostForm)

	if f.revokeStatus != 0 {
		w.WriteHeader(f.revokeStatus)
	}
}

func (f *fakeIssuer) keys(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &f.key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
	}})
}

func (f *fakeIssuer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f.mu.Lock()
	f.authorizes = append(f.authorizes, q)
	code := "code-" + q.Get("state")
	f.codes[code] = fakeCode{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri")}
	f.lastAudience = q.Get("audience")
	f.mu.Unlock()

	u, _ := url.Parse(q.Get("redirect_uri"))
	rq := u.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	u.RawQuery = rq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (f *fakeIssuer) idToken(aud, nonce string) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: f.key, KeyID: "k1"}}, nil)
	if err != nil {
		f.t.Fatal(err)
	}

	now := time.Now()
	claims := map[string]any{
		"iss": f.srv.URL, "sub": "alice", "aud": aud,
		"iat": now.Unix(), "exp": now.Add(f.idTokenTTL).Unix(),
	}

	if nonce != "" {
		claims["nonce"] = nonce
	}

	tok, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		f.t.Fatal(err)
	}

	return tok
}

func (f *fakeIssuer) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()

	f.mu.Lock()
	defer f.mu.Unlock()

	fail := func(msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	if r.Form.Get("client_secret") != "" || r.Header.Get("Authorization") != "" {
		fail("public client must not authenticate")

		return
	}

	aud := r.Form.Get("client_id")
	resp := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 300}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		c, ok := f.codes[r.Form.Get("code")]
		if !ok || c.redirect != r.Form.Get("redirect_uri") {
			fail("invalid_grant")

			return
		}

		if oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != c.challenge {
			fail("invalid_grant")

			return
		}

		if f.lastAudience != "" && !f.ignoreAud {
			aud = f.lastAudience
		}

		nonce := c.nonce
		if f.wrongNonce {
			nonce = "wrong-nonce"
		}

		resp["id_token"] = f.idToken(aud, nonce)
		resp["refresh_token"] = fakeRefreshToken
	case "refresh_token":
		if f.failRefresh || r.Form.Get("refresh_token") != fakeRefreshToken {
			fail("invalid_grant")

			return
		}

		f.refreshes++

		if f.lastAudience != "" && !f.ignoreAud {
			aud = f.lastAudience
		}

		resp["id_token"] = f.idToken(aud, "")
	default:
		fail("unsupported_grant_type")

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// fakeBrowser stands in for the user's browser: it follows the authorize URL,
// which redirects to the CLI's loopback listener.
func fakeBrowser(opened *int) func(string) error {
	return func(u string) error {
		*opened++

		go func() {
			resp, err := http.Get(u) //nolint:gosec,noctx // test double for a browser
			if err == nil {
				_ = resp.Body.Close()
			}
		}()

		return nil
	}
}

func newTestFlow(t *testing.T, iss *fakeIssuer, opened *int) (*authFlow, *bytes.Buffer) {
	t.Helper()

	var stderr bytes.Buffer

	return &authFlow{
		Issuer: iss.srv.URL, ClientID: "ichor-cli", Dir: t.TempDir() + "/auth",
		Stderr: &stderr, OpenBrowser: fakeBrowser(opened), LoginTimeout: 10 * time.Second,
	}, &stderr
}

func interactiveInfo(b bool) string { return fmt.Sprintf(execInfoV1, b) }

func TestAuthLoginLoopbackPKCE(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, stderr := newTestFlow(t, iss, &opened)

	if err := f.loginAndSave(context.Background()); err != nil {
		t.Fatal(err) // the fake rejects a wrong verifier, redirect or missing PKCE
	}

	if len(iss.authorizes) != 1 || opened != 1 {
		t.Fatalf("authorizes=%d opened=%d", len(iss.authorizes), opened)
	}

	q := iss.authorizes[0]
	for k, want := range map[string]string{
		"response_type": "code", "code_challenge_method": "S256", "client_id": "ichor-cli",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}

	if !strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:") || !strings.HasSuffix(q.Get("redirect_uri"), "/callback") {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}

	if q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Errorf("missing state/nonce/challenge: %v", q)
	}

	if !strings.Contains(q.Get("scope"), "openid") || !strings.Contains(q.Get("scope"), "offline_access") {
		t.Errorf("scope = %q", q.Get("scope"))
	}

	e, err := loadAuthCache(f.Dir, f.key())
	if err != nil || e == nil || e.RefreshToken != fakeRefreshToken {
		t.Fatalf("cache = %+v, %v", e, err)
	}

	if strings.Contains(stderr.String(), e.IDToken) || strings.Contains(stderr.String(), fakeRefreshToken) {
		t.Error("a token was written to stderr")
	}
}

func TestAuthLoginRejectsWrongNonce(t *testing.T) {
	iss := newFakeIssuer(t)
	iss.wrongNonce = true

	var opened int

	f, _ := newTestFlow(t, iss, &opened)

	if err := f.loginAndSave(context.Background()); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("err = %v, want a nonce error", err)
	}

	if e, _ := loadAuthCache(f.Dir, f.key()); e != nil {
		t.Error("a rejected login was cached")
	}
}

func TestAuthCallbackHandlerStateCheck(t *testing.T) {
	ch := make(chan authCallback, 1)
	h := authCallbackHandler("good", ch)

	do := func(target string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

		return rec.Code
	}

	if c := do("/callback?code=x&state=evil"); c != http.StatusBadRequest {
		t.Errorf("wrong state: %d", c)
	}

	if c := do("/other?code=x&state=good"); c != http.StatusNotFound {
		t.Errorf("wrong path: %d", c)
	}

	select {
	case r := <-ch:
		t.Fatalf("a rejected request was delivered: %+v", r)
	default:
	}

	if c := do("/callback?code=abc&state=good"); c != http.StatusOK {
		t.Errorf("good: %d", c)
	}

	if r := <-ch; r.code != "abc" || r.err != nil {
		t.Errorf("result = %+v", r)
	}

	// An error from the issuer with the right state is reported, once.
	ch2 := make(chan authCallback, 1)
	rec := httptest.NewRecorder()
	authCallbackHandler("s", ch2).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?error=access_denied&state=s", nil))

	if r := <-ch2; r.err == nil || !strings.Contains(r.err.Error(), "access_denied") {
		t.Errorf("result = %+v", r)
	}
}

func TestAuthTokenExecCredential(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, stderr := newTestFlow(t, iss, &opened)

	var out bytes.Buffer
	if err := runAuthToken(context.Background(), f, interactiveInfo(true), &out); err != nil {
		t.Fatal(err)
	}

	var cred clientauthv1.ExecCredential
	if err := json.Unmarshal(out.Bytes(), &cred); err != nil {
		t.Fatalf("stdout is not one ExecCredential: %v\n%s", err, out.String())
	}

	if cred.APIVersion != "client.authentication.k8s.io/v1" || cred.Kind != "ExecCredential" {
		t.Errorf("type = %s %s", cred.APIVersion, cred.Kind)
	}

	e, _ := loadAuthCache(f.Dir, f.key())
	if cred.Status == nil || cred.Status.Token != e.IDToken || cred.Status.ExpirationTimestamp == nil {
		t.Fatalf("status = %+v", cred.Status)
	}

	if !cred.Status.ExpirationTimestamp.Time.Equal(e.Expiry.Truncate(time.Second)) {
		t.Errorf("expirationTimestamp %v != token exp %v", cred.Status.ExpirationTimestamp, e.Expiry)
	}

	if strings.Contains(stderr.String(), cred.Status.Token) {
		t.Error("the token was written to stderr")
	}

	// Second run: cached, no browser, no issuer round trip.
	out.Reset()

	if err := runAuthToken(context.Background(), f, interactiveInfo(false), &out); err != nil {
		t.Fatal(err)
	}

	if opened != 1 || len(iss.authorizes) != 1 || iss.refreshes != 0 {
		t.Errorf("opened=%d authorizes=%d refreshes=%d", opened, len(iss.authorizes), iss.refreshes)
	}
}

func TestAuthTokenNeverLogsInUnlessInteractive(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, _ := newTestFlow(t, iss, &opened)

	for _, info := range []string{"", interactiveInfo(false)} {
		var out bytes.Buffer
		if err := runAuthToken(context.Background(), f, info, &out); err == nil || !strings.Contains(err.Error(), "ichor auth login") || !strings.Contains(err.Error(), "--audience") || !strings.Contains(err.Error(), "from a terminal") {
			t.Errorf("info %q: err = %v", info, err)
		}

		if out.Len() != 0 {
			t.Errorf("stdout not empty on failure: %q", out.String())
		}
	}

	if opened != 0 {
		t.Error("a browser was opened in a non-interactive session")
	}

	err := runAuthToken(context.Background(), f, `{"apiVersion":"client.authentication.k8s.io/v1beta1","spec":{"interactive":true}}`, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "v1beta1") {
		t.Errorf("err = %v, want an apiVersion error", err)
	}
}

func TestAuthTokenRefreshesExpiredIDToken(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, _ := newTestFlow(t, iss, &opened)

	if err := f.loginAndSave(context.Background()); err != nil {
		t.Fatal(err)
	}

	old, _ := loadAuthCache(f.Dir, f.key())

	// Within the 30 s skew of expiry counts as expired.
	f.Now = func() time.Time { return old.Expiry.Add(-10 * time.Second) }

	var out bytes.Buffer
	if err := runAuthToken(context.Background(), f, interactiveInfo(false), &out); err != nil {
		t.Fatal(err)
	}

	if iss.refreshes != 1 || opened != 1 {
		t.Fatalf("refreshes=%d opened=%d", iss.refreshes, opened)
	}

	n, _ := loadAuthCache(f.Dir, f.key())
	if n.IDToken == old.IDToken && !n.Expiry.After(old.Expiry) {
		t.Error("the cache still holds the old ID token")
	}

	if n.RefreshToken != fakeRefreshToken {
		t.Errorf("refresh token lost: %q", n.RefreshToken)
	}

	// 29 s left is refreshed, 31 s left is served from cache.
	f.Now = func() time.Time { return n.Expiry.Add(-31 * time.Second) }

	if err := runAuthToken(context.Background(), f, interactiveInfo(false), &bytes.Buffer{}); err != nil || iss.refreshes != 1 {
		t.Errorf("err=%v refreshes=%d", err, iss.refreshes)
	}
}

func TestAuthTokenFailedRefresh(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, stderr := newTestFlow(t, iss, &opened)

	if err := f.loginAndSave(context.Background()); err != nil {
		t.Fatal(err)
	}

	e, _ := loadAuthCache(f.Dir, f.key())
	f.Now = func() time.Time { return e.Expiry.Add(time.Hour) }
	iss.failRefresh = true

	if err := runAuthToken(context.Background(), f, interactiveInfo(false), &bytes.Buffer{}); err == nil {
		t.Fatal("non-interactive run succeeded with a dead refresh token")
	}

	if err := runAuthToken(context.Background(), f, interactiveInfo(true), &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive run did not fall back to login: %v", err)
	}

	if opened != 2 {
		t.Errorf("opened = %d, want 2 (login, then fallback login)", opened)
	}

	if strings.Contains(stderr.String(), fakeRefreshToken) {
		t.Error("a refresh token was written to stderr")
	}
}

func TestAuthAudienceIsRequestedAndChecked(t *testing.T) {
	iss := newFakeIssuer(t)

	var opened int

	f, _ := newTestFlow(t, iss, &opened)
	f.Audience = "ichor-kube"

	if err := f.loginAndSave(context.Background()); err != nil {
		t.Fatal(err)
	}

	if iss.lastAudience != "ichor-kube" {
		t.Errorf("audience param = %q", iss.lastAudience)
	}

	// An issuer that ignores the audience hint is rejected.
	iss.ignoreAud = true
	f.Audience = "ichor-ui"

	if err := f.loginAndSave(context.Background()); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Errorf("err = %v, want an audience error", err)
	}
}

func TestAuthCachePermissionsAndLogout(t *testing.T) {
	dir := t.TempDir() + "/auth"
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // a too-open directory must be tightened
		t.Fatal(err)
	}

	key := authCacheKey("https://i", "ichor-cli", "")
	if err := saveAuthCache(dir, key, &authCacheEntry{IDToken: "x", RefreshToken: "y"}); err != nil {
		t.Fatal(err)
	}

	unlock, err := lockAuthCache(dir, key)
	if err != nil {
		t.Fatal(err)
	}

	unlock()

	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, %v", st.Mode().Perm(), err)
	}

	entries, _ := os.ReadDir(dir)
	for _, de := range entries {
		info, _ := de.Info()
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", de.Name(), info.Mode().Perm())
		}
	}

	if authCacheKey("https://i", "ichor-cli", "a") == key {
		t.Error("the audience is not part of the cache key")
	}

	if err := deleteAuthCache(dir, key); err != nil {
		t.Fatal(err)
	}

	if e, err := loadAuthCache(dir, key); e != nil || err != nil {
		t.Errorf("after logout: %v, %v", e, err)
	}

	if err := deleteAuthCache(dir, key); err != nil {
		t.Errorf("logout of nothing: %v", err)
	}
}
