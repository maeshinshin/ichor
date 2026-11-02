package oidcissuer_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/internal/oidctrust"
)

const (
	uiRedirect  = "https://ichor.example.test/auth/callback"
	cliRedirect = "http://127.0.0.1:43211/callback"
	externalURL = "https://ichor.example.test"
)

// dir is a fake user directory and password verifier.
type dir struct {
	mu    sync.Mutex
	users map[string]oidcissuer.User
	pw    map[string]string
}

func newDir() *dir {
	return &dir{users: map[string]oidcissuer.User{}, pw: map[string]string{}}
}

func (d *dir) set(u oidcissuer.User, password string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.users[u.Name] = u
	d.pw[u.Name] = password
}

func (d *dir) LookupUser(_ context.Context, name string) (oidcissuer.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	u, ok := d.users[name]
	if !ok {
		return oidcissuer.User{}, oidcissuer.ErrUserNotFound
	}
	return u, nil
}

func (d *dir) VerifyPassword(_ context.Context, name, password string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pw[name]
	return ok && p == password, nil
}

type clock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.off)
}

func (c *clock) shift(d time.Duration) {
	c.mu.Lock()
	c.off += d
	c.mu.Unlock()
}

func keyRecord(t *testing.T, kid string, activeFrom time.Time) oidctrust.Key {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return oidctrust.Key{
		KID: kid, CreatedAt: activeFrom, ActiveFrom: activeFrom,
		PEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	}
}

func keysJSON(t *testing.T, recs ...oidctrust.Key) []byte {
	t.Helper()
	b, err := oidctrust.Keys{Keys: recs}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type env struct {
	t    *testing.T
	iss  *oidcissuer.Issuer
	h    http.Handler
	dir  *dir
	keys *oidcissuer.KeyStore
	recs []oidctrust.Key
	clk  *clock
}

// newEnv builds an issuer with user alice (password "correct horse", roles
// admin) and one signing key.
func newEnv(t *testing.T, issuer, internalJWKS string) *env {
	t.Helper()
	return newEnvWith(t, issuer, internalJWKS, nil)
}

// newEnvWith is newEnv with a last look at the issuer's Config.
func newEnvWith(t *testing.T, issuer, internalJWKS string, mutate func(*oidcissuer.Config)) *env {
	t.Helper()
	clk := &clock{}
	keys := oidcissuer.NewKeyStore(clk.now)
	recs := []oidctrust.Key{keyRecord(t, "k1", time.Now().Add(-time.Hour))}
	if err := keys.Update(keysJSON(t, recs...)); err != nil {
		t.Fatal(err)
	}
	d := newDir()
	d.set(oidcissuer.User{Name: userAlice, Roles: []string{roleAdmin}}, "correct horse")
	cfg := oidcissuer.Config{
		Issuer: issuer, InternalJWKSURI: internalJWKS, UIRedirectURI: uiRedirect,
		Keys: keys, Users: d, Passwords: d, Now: clk.now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	iss, err := oidcissuer.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, iss: iss, h: iss.Handler(), dir: d, keys: keys, recs: recs, clk: clk}
}

// startTLS serves whatever handler is later passed to the returned setter.
func startTLS(t *testing.T) (*httptest.Server, func(http.Handler)) {
	t.Helper()
	var hp atomic.Pointer[http.Handler]
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*hp.Load()).ServeHTTP(w, r)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, func(h http.Handler) { hp.Store(&h) }
}

type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	v := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(v))
	return pkce{v, base64.RawURLEncoding.EncodeToString(sum[:])}
}

func (e *env) do(h http.Handler, method, path, ctype, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *env) authorize(client, redirect, aud string, p pkce) *httptest.ResponseRecorder {
	q := url.Values{
		pRespType: {pCode}, pClientID: {client}, pRedirect: {redirect},
		pScope: {"openid offline_access"}, pState: {stateVal}, pNonce: {"n1"},
		pChallenge: {p.challenge}, pMethod: {pS256},
	}
	if aud != "" {
		q.Set(pAudience, aud)
	}
	return e.do(e.h, http.MethodGet, "/oidc/authorize?"+q.Encode(), "", "")
}

func (e *env) ticket(rec *httptest.ResponseRecorder) string {
	e.t.Helper()
	if rec.Code != http.StatusFound {
		e.t.Fatalf("authorize: status %d body %q", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || loc.Query().Get(pTicket) == "" {
		e.t.Fatalf("authorize: bad Location %q", rec.Header().Get("Location"))
	}
	return loc.Query().Get(pTicket)
}

func (e *env) login(ticket, user, password string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{pTicket: ticket, pUsername: user, pPassword: password})
	return e.do(e.h, http.MethodPost, "/oidc/login", "application/json", string(b))
}

// code runs authorize and login as alice and returns the authorization code.
func (e *env) code(client, redirect, aud string, p pkce) string {
	e.t.Helper()
	rec := e.login(e.ticket(e.authorize(client, redirect, aud, p)), userAlice, "correct horse")
	if rec.Code != http.StatusOK {
		e.t.Fatalf("login: status %d body %q", rec.Code, rec.Body.String())
	}
	var out struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatal(err)
	}
	u, err := url.Parse(out.RedirectURI)
	if err != nil {
		e.t.Fatal(err)
	}
	if got := u.Query().Get(pState); got != stateVal {
		e.t.Fatalf("state = %q", got)
	}
	if got := u.Query().Get("iss"); got == "" {
		e.t.Fatal("missing iss in redirect")
	}
	return u.Query().Get("code")
}

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func (e *env) token(form url.Values) (int, tokenResp) {
	rec := e.do(e.h, http.MethodPost, "/oidc/token", "application/x-www-form-urlencoded", form.Encode())
	var tr tokenResp
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	return rec.Code, tr
}

func codeForm(client, redirect, code string, p pkce) url.Values {
	return url.Values{
		pGrant: {"authorization_code"}, pClientID: {client}, pRedirect: {redirect},
		"code": {code}, "code_verifier": {p.verifier},
	}
}

func refreshForm(client, refresh, aud string) url.Values {
	f := url.Values{pGrant: {pRefresh}, pClientID: {client}, pRefresh: {refresh}}
	if aud != "" {
		f.Set(pAudience, aud)
	}
	return f
}

// tokens runs the whole flow and requires success.
func (e *env) tokens(client, redirect, aud string) tokenResp {
	e.t.Helper()
	p := newPKCE()
	status, tr := e.token(codeForm(client, redirect, e.code(client, redirect, aud, p), p))
	if status != http.StatusOK {
		e.t.Fatalf("token: status %d error %q", status, tr.Error)
	}
	return tr
}

func claimsOf(t *testing.T, jws string) map[string]any {
	t.Helper()
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWS: %d parts", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

const (
	clientUI  = "ichor-ui"
	clientCLI = "ichor-cli"
	audKube   = "ichor-kube"
	userAlice = "alice"
	roleAdmin = "admin"
	stateVal  = "st1"
	pClientID = "client_id"
	pNonce    = "nonce"
	pS256     = "S256"
	pPassword = "password"
	pGrant    = "grant_type"

	pCode      = "code"
	pScope     = "scope"
	pChallenge = "code_challenge"
)

const (
	pMethod   = "code_challenge_method"
	pRedirect = "redirect_uri"
	pState    = "state"
	pRespType = "response_type"
	pRefresh  = "refresh_token"
	pAudience = "audience"
	pTicket   = "ticket"
	pUsername = "username"
)
