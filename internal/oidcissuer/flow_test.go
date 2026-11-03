package oidcissuer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const scopeOpenID = "openid"

func TestRedirectRules(t *testing.T) {
	e := newEnv(t, externalURL, "")
	cases := []struct {
		client, uri string
		ok          bool
	}{
		{clientUI, uiRedirect, true},
		{clientUI, uiRedirect + "/", false},
		{clientUI, "https://evil.example/auth/callback", false},
		{clientUI, cliRedirect, false},
		{clientCLI, "http://127.0.0.1:8123/callback", true},
		{clientCLI, "http://127.0.0.1:1/callback", true},
		{clientCLI, "http://127.0.0.1:65535/callback", true},
		{clientCLI, "http://localhost:8123/callback", false},
		{clientCLI, "https://127.0.0.1:8123/callback", false},
		{clientCLI, "http://127.0.0.1/callback", false},
		{clientCLI, "http://127.0.0.1:8123/other", false},
		{clientCLI, "http://127.0.0.1:8123/callback?x=1", false},
		{clientCLI, "http://127.0.0.1:8123/callback#f", false},
		{clientCLI, "http://[::1]:8123/callback", false},
		{clientCLI, "http://127.0.0.1:0/callback", false},
		{clientCLI, "http://127.0.0.1:99999/callback", false},
		{clientCLI, "http://127.0.0.1:08123/callback", false},
		{clientCLI, "http://127.0.0.2:8123/callback", false},
		{clientCLI, "http://127.0.0.1:8123@evil.example/callback", false},
		{clientCLI, "http://u:p@127.0.0.1:8123/callback", false},
		{clientCLI, uiRedirect, false},
		{"other", cliRedirect, false},
	}
	for _, c := range cases {
		rec := e.authorize(c.client, c.uri, "", newPKCE())
		want := http.StatusBadRequest
		if c.ok {
			want = http.StatusFound
		}
		if rec.Code != want {
			t.Errorf("%s %q: status %d, want %d", c.client, c.uri, rec.Code, want)
		}
		if !c.ok && rec.Header().Get("Location") != "" {
			t.Errorf("%s %q: redirected to an unvalidated URI", c.client, c.uri)
		}
	}
}

func TestAuthorizeRejectsBadRequests(t *testing.T) {
	e := newEnv(t, externalURL, "")
	p := newPKCE()
	base := authQuery(clientCLI, cliRedirect, scopeOpenID, p)
	mut := func(f func(url.Values)) url.Values {
		v := url.Values{}
		for k, x := range base {
			v[k] = slices.Clone(x)
		}
		f(v)
		return v
	}
	cases := map[string]url.Values{
		"plain pkce":      mut(func(v url.Values) { v.Set(pMethod, "plain") }),
		"no pkce":         mut(func(v url.Values) { v.Del(pChallenge) }),
		"short challenge": mut(func(v url.Values) { v.Set(pChallenge, "abc") }),
		"implicit":        mut(func(v url.Values) { v.Set(pRespType, "token") }),
		"hybrid":          mut(func(v url.Values) { v.Set(pRespType, "code id_token") }),
		"no openid":       mut(func(v url.Values) { v.Set(pScope, "profile") }),
		"no state":        mut(func(v url.Values) { v.Del(pState) }),
		"no nonce":        mut(func(v url.Values) { v.Del(pNonce) }),
		"bad audience":    mut(func(v url.Values) { v.Set(pAudience, "somebody-else") }),
		"ui asks kube": mut(func(v url.Values) {
			v.Set(pClientID, clientUI)
			v.Set(pRedirect, uiRedirect)
			v.Set(pAudience, audKube)
		}),
	}
	for name, q := range cases {
		rec := e.do(e.h, http.MethodGet, "/oidc/authorize?"+q.Encode(), "", "")
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusFound || loc.Query().Get("error") == "" || loc.Query().Get(pTicket) != "" {
			t.Errorf("%s: status %d location %q", name, rec.Code, rec.Header().Get("Location"))
		}
		if loc != nil && !strings.HasPrefix(loc.String(), strings.Split(q.Get(pRedirect), "?")[0]) {
			t.Errorf("%s: error redirected to %q", name, loc)
		}
	}
	dup := base.Encode() + "&state=other"
	if rec := e.do(e.h, http.MethodGet, "/oidc/authorize?"+dup, "", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("repeated parameter: status %d", rec.Code)
	}
}

// The in-page sign-in: Accept: application/json answers the ticket as JSON,
// with the same checks as the redirect path and no redirect on any failure.
func TestAuthorizeJSON(t *testing.T) {
	e := newEnv(t, externalURL, "")
	get := func(client, redirect string, mut func(url.Values)) *httptest.ResponseRecorder {
		q := authQuery(client, redirect, scopeOpenID, newPKCE())
		if mut != nil {
			mut(q)
		}
		req := httptest.NewRequest(http.MethodGet, "/oidc/authorize?"+q.Encode(), nil)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	rec := get(clientUI, uiRedirect, nil)
	var out struct{ Ticket string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Ticket == "" || rec.Header().Get("Location") != "" {
		t.Fatalf("good request: %d %q", rec.Code, rec.Body.String())
	}
	bad := map[string]*httptest.ResponseRecorder{
		"bad redirect": get(clientUI, "https://evil.example/auth/callback", nil),
		"bad client":   get("other", uiRedirect, nil),
		"plain pkce":   get(clientUI, uiRedirect, func(v url.Values) { v.Set(pMethod, "plain") }),
		"no nonce":     get(clientUI, uiRedirect, func(v url.Values) { v.Del(pNonce) }),
	}
	for name, rec := range bad {
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" || strings.Contains(rec.Body.String(), "ticket") {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body.String())
		}
	}
}

func TestCodeGrantNegatives(t *testing.T) {
	e := newEnv(t, externalURL, "")
	p := newPKCE()

	t.Run("replay", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		if s, _ := e.token(codeForm(clientCLI, cliRedirect, code, p)); s != http.StatusOK {
			t.Fatalf("first use: %d", s)
		}
		if s, tr := e.token(codeForm(clientCLI, cliRedirect, code, p)); s != http.StatusBadRequest || tr.Error != "invalid_grant" {
			t.Fatalf("replay: %d %q", s, tr.Error)
		}
	})
	t.Run("wrong verifier", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		if s, tr := e.token(codeForm(clientCLI, cliRedirect, code, newPKCE())); s != http.StatusBadRequest || tr.Error != "invalid_grant" {
			t.Fatalf("%d %q", s, tr.Error)
		}
		// the attempt burned the code
		if s, _ := e.token(codeForm(clientCLI, cliRedirect, code, p)); s == http.StatusOK {
			t.Fatal("code survived a failed PKCE check")
		}
	})
	t.Run("short verifier", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		f := codeForm(clientCLI, cliRedirect, code, p)
		f.Set("code_verifier", "x")
		if s, _ := e.token(f); s != http.StatusBadRequest {
			t.Fatal(s)
		}
	})
	t.Run("redirect mismatch", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		if s, _ := e.token(codeForm(clientCLI, "http://127.0.0.1:9999/callback", code, p)); s != http.StatusBadRequest {
			t.Fatal(s)
		}
	})
	t.Run("other client", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		if s, _ := e.token(codeForm(clientUI, uiRedirect, code, p)); s != http.StatusBadRequest {
			t.Fatal(s)
		}
	})
	t.Run("expired", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		e.clk.shift(ichorauth.DefaultCodeTTL + time.Second)
		defer e.clk.shift(-ichorauth.DefaultCodeTTL - time.Second)
		if s, _ := e.token(codeForm(clientCLI, cliRedirect, code, p)); s != http.StatusBadRequest {
			t.Fatal(s)
		}
	})
	t.Run("wrong token kinds", func(t *testing.T) {
		tr := e.tokens(clientCLI, cliRedirect, "")
		// refresh token and ID token are no codes
		for _, bad := range []string{tr.RefreshToken, tr.IDToken, "garbage", ""} {
			if s, _ := e.token(codeForm(clientCLI, cliRedirect, bad, p)); s != http.StatusBadRequest {
				t.Fatalf("%.10q accepted as code: %d", bad, s)
			}
		}
		// a ticket is no code
		ticket := e.ticket(e.authorize(clientCLI, cliRedirect, "", p))
		if s, _ := e.token(codeForm(clientCLI, cliRedirect, ticket, p)); s != http.StatusBadRequest {
			t.Fatal("ticket accepted as code")
		}
		// a code and an ID token are no refresh tokens
		code := e.code(clientCLI, cliRedirect, "", p)
		for _, bad := range []string{code, tr.IDToken, ticket} {
			if s, _ := e.token(refreshForm(clientCLI, bad, "")); s != http.StatusBadRequest {
				t.Fatalf("%.10q accepted as refresh token: %d", bad, s)
			}
		}
	})
	t.Run("grants and clients", func(t *testing.T) {
		if s, _ := e.token(url.Values{pGrant: {pPassword}, pClientID: {clientCLI}, pUsername: {userAlice}, pPassword: {"correct horse"}}); s != http.StatusBadRequest {
			t.Fatalf("password grant: %d", s)
		}
		if s, _ := e.token(url.Values{pGrant: {"client_credentials"}, pClientID: {clientCLI}}); s != http.StatusBadRequest {
			t.Fatalf("client_credentials: %d", s)
		}
		if s, _ := e.token(refreshForm("nobody", "x", "")); s != http.StatusUnauthorized {
			t.Fatalf("unknown client: %d", s)
		}
		if rec := e.do(e.h, http.MethodPost, "/oidc/token", "application/json", "{}"); rec.Code != http.StatusBadRequest {
			t.Fatalf("json body: %d", rec.Code)
		}
	})
	t.Run("token response headers", func(t *testing.T) {
		code := e.code(clientCLI, cliRedirect, "", p)
		rec := e.do(e.h, http.MethodPost, "/oidc/token", "application/x-www-form-urlencoded", codeForm(clientCLI, cliRedirect, code, p).Encode())
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" {
			t.Fatalf("headers %v", rec.Header())
		}
	})
}

func TestLoginNegatives(t *testing.T) {
	e := newEnv(t, externalURL, "")
	p := newPKCE()
	ticket := e.ticket(e.authorize(clientCLI, cliRedirect, "", p))

	if rec := e.login("garbage", userAlice, "correct horse"); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage ticket: %d", rec.Code)
	}
	parts := strings.Split(ticket, ".")
	parts[1] = parts[1][:len(parts[1])-2] + "AA"
	if rec := e.login(strings.Join(parts, "."), userAlice, "correct horse"); rec.Code != http.StatusBadRequest {
		t.Errorf("tampered ticket: %d", rec.Code)
	}
	if rec := e.login(ticket, userAlice, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
	if rec := e.login(ticket, "nobody", "x"); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "nobody") {
		t.Errorf("unknown user: %d %q", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.h, http.MethodPost, "/oidc/login", "application/x-www-form-urlencoded", "ticket="+ticket+"&username=alice&password=correct+horse"); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form login: %d", rec.Code)
	}
	if rec := e.do(e.h, http.MethodGet, "/oidc/login", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET login: %d", rec.Code)
	}
	e.dir.set(oidcissuer.User{Name: "bob", Disabled: true}, "pw-for-bob")
	if rec := e.login(ticket, "bob", "pw-for-bob"); rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled user: %d", rec.Code)
	}
	if rec := e.login(ticket, "bob", "pw-for-bob"); strings.Contains(rec.Body.String(), "disabled") {
		t.Error("disabled state leaked")
	}
	e.clk.shift(ichorauth.DefaultTicketTTL + time.Second)
	if rec := e.login(ticket, userAlice, "correct horse"); rec.Code != http.StatusBadRequest {
		t.Errorf("expired ticket: %d", rec.Code)
	}
}

func TestLoginBackoff(t *testing.T) {
	e := newEnv(t, externalURL, "")
	ticket := e.ticket(e.authorize(clientCLI, cliRedirect, "", newPKCE()))
	for i := range 5 {
		if rec := e.login(ticket, userAlice, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, rec.Code)
		}
	}
	rec := e.login(ticket, userAlice, "correct horse")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after five failures: %d", rec.Code)
	}
	e.clk.shift(2 * time.Second)
	if rec := e.login(ticket, userAlice, "correct horse"); rec.Code != http.StatusOK {
		t.Fatalf("after waiting: %d", rec.Code)
	}
}

func TestRefreshGrant(t *testing.T) {
	e := newEnv(t, externalURL, "")
	tr := e.tokens(clientCLI, cliRedirect, "")
	if tr.RefreshToken == "" {
		t.Fatal("no refresh token despite offline_access")
	}
	aud := func(tok string) any { return claimsOf(t, tok)["aud"] }

	if aud(tr.IDToken) != audKube {
		t.Fatalf("cli default audience %v", aud(tr.IDToken))
	}
	s, r1 := e.token(refreshForm(clientCLI, tr.RefreshToken, ""))
	if s != http.StatusOK || aud(r1.IDToken) != audKube || r1.RefreshToken != "" {
		t.Fatalf("refresh: %d aud %v refresh %q", s, aud(r1.IDToken), r1.RefreshToken)
	}
	if _, r2 := e.token(refreshForm(clientCLI, tr.RefreshToken, clientUI)); aud(r2.IDToken) != clientUI {
		t.Fatalf("audience parameter ignored: %v", aud(r2.IDToken))
	}
	if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "other")); s != http.StatusBadRequest {
		t.Fatalf("foreign audience: %d", s)
	}
	if s, _ := e.token(refreshForm(clientUI, tr.RefreshToken, "")); s != http.StatusBadRequest {
		t.Fatalf("refresh token used by another client: %d", s)
	}
	ui := e.tokens(clientUI, uiRedirect, "")
	if s, _ := e.token(refreshForm(clientUI, ui.RefreshToken, audKube)); s != http.StatusBadRequest {
		t.Fatalf("ui client got the kube audience: %d", s)
	}

	t.Run("role change shows at refresh", func(t *testing.T) {
		e.dir.set(oidcissuer.User{Name: userAlice, Roles: []string{"viewer", "ops"}}, "correct horse")
		_, r := e.token(refreshForm(clientCLI, tr.RefreshToken, ""))
		g, _ := claimsOf(t, r.IDToken)["groups"].([]any)
		if len(g) != 2 || g[0] != "viewer" {
			t.Fatalf("groups %v", g)
		}
	})
	t.Run("not before", func(t *testing.T) {
		e.dir.set(oidcissuer.User{Name: userAlice, NotBefore: e.clk.now().Add(-time.Hour)}, "correct horse")
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusOK {
			t.Fatalf("older notBefore: %d", s)
		}
		e.dir.set(oidcissuer.User{Name: userAlice, NotBefore: e.clk.now().Add(2 * time.Second)}, "correct horse")
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusBadRequest {
			t.Fatalf("iat < notBefore: %d", s)
		}
	})
	t.Run("disabled and deleted", func(t *testing.T) {
		e.dir.set(oidcissuer.User{Name: userAlice, Disabled: true}, "correct horse")
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusBadRequest {
			t.Fatalf("disabled: %d", s)
		}
		e.dir.mu.Lock()
		delete(e.dir.users, userAlice)
		e.dir.mu.Unlock()
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusBadRequest {
			t.Fatalf("deleted: %d", s)
		}
	})
	t.Run("absolute lifetime", func(t *testing.T) {
		e.dir.set(oidcissuer.User{Name: userAlice, Roles: []string{roleAdmin}}, "correct horse")
		e.clk.shift(11 * time.Hour)
		if s, _ := e.token(refreshForm("ui-or-cli", tr.RefreshToken, "")); s == http.StatusOK {
			t.Fatal("bad client accepted")
		}
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusOK {
			t.Fatalf("within lifetime: %d", s)
		}
		e.clk.shift(2 * time.Hour)
		if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusBadRequest {
			t.Fatalf("past 12h: %d", s)
		}
	})
}

func TestMustChangeGetsNoKubernetesToken(t *testing.T) {
	e := newEnv(t, externalURL, "")
	tr := e.tokens(clientCLI, cliRedirect, clientUI)

	e.dir.set(oidcissuer.User{Name: userAlice, MustChange: true}, "correct horse")

	if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, audKube)); s != http.StatusForbidden {
		t.Fatalf("kube audience for a must-change user: %d", s)
	}

	if s, r := e.token(refreshForm(clientCLI, tr.RefreshToken, "")); s != http.StatusOK || claimsOf(t, r.IDToken)["aud"] != clientUI {
		t.Fatalf("ui audience must still work to change the password: %d", s)
	}

	e.dir.set(oidcissuer.User{Name: userAlice}, "correct horse")

	if s, _ := e.token(refreshForm(clientCLI, tr.RefreshToken, audKube)); s != http.StatusOK {
		t.Fatalf("kube audience after the change: %d", s)
	}
}

func TestAudienceSurvivesRefresh(t *testing.T) {
	e := newEnv(t, externalURL, "")
	tr := e.tokens(clientCLI, cliRedirect, clientUI)
	if claimsOf(t, tr.IDToken)["aud"] != clientUI {
		t.Fatal("login audience ignored")
	}
	_, r := e.token(refreshForm(clientCLI, tr.RefreshToken, ""))
	if claimsOf(t, r.IDToken)["aud"] != clientUI {
		t.Fatalf("refresh lost the chosen audience: %v", claimsOf(t, r.IDToken)["aud"])
	}
	_, r = e.token(refreshForm(clientCLI, tr.RefreshToken, audKube))
	if claimsOf(t, r.IDToken)["aud"] != audKube {
		t.Fatal("explicit audience on refresh ignored")
	}
}

func TestNoOfflineAccessNoRefreshToken(t *testing.T) {
	e := newEnv(t, externalURL, "")
	p := newPKCE()
	q := authQuery(clientCLI, cliRedirect, scopeOpenID, p)
	code := e.codeFrom(e.do(e.h, http.MethodGet, "/oidc/authorize?"+q.Encode(), "", ""))
	s, tr := e.token(codeForm(clientCLI, cliRedirect, code, p))
	if s != http.StatusOK || tr.RefreshToken != "" {
		t.Fatalf("%d refresh=%q", s, tr.RefreshToken)
	}
}

func TestDiscoveryVariants(t *testing.T) {
	e := newEnv(t, externalURL, "https://10.96.0.7:8444/oidc/jwks")
	ext := e.do(e.h, http.MethodGet, "/.well-known/openid-configuration", "", "")
	in := e.do(e.iss.InternalHandler(), http.MethodGet, "/.well-known/openid-configuration", "", "")
	var de, di map[string]any
	if json.Unmarshal(ext.Body.Bytes(), &de) != nil || json.Unmarshal(in.Body.Bytes(), &di) != nil {
		t.Fatal("bad discovery json")
	}
	if de["issuer"] != externalURL || di["issuer"] != externalURL {
		t.Fatalf("issuer %v / %v", de["issuer"], di["issuer"])
	}
	if de["jwks_uri"] != externalURL+"/oidc/jwks" || di["jwks_uri"] != "https://10.96.0.7:8444/oidc/jwks" {
		t.Fatalf("jwks_uri %v / %v", de["jwks_uri"], di["jwks_uri"])
	}
	if m, _ := de["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != pS256 {
		t.Fatalf("pkce methods %v", m)
	}
	for _, p := range []string{"/oidc/token", "/oidc/authorize", "/oidc/login"} {
		if rec := e.do(e.iss.InternalHandler(), http.MethodPost, p, "application/json", "{}"); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("internal handler serves %s: %d", p, rec.Code)
		}
	}
}

func TestJWKSPublishesOnlyPublicKeys(t *testing.T) {
	e := newEnv(t, externalURL, "")
	rec := e.do(e.h, http.MethodGet, "/oidc/jwks", "", "")
	body := rec.Body.String()
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Cache-Control %q delays apiserver rotation pickup", rec.Header().Get("Cache-Control"))
	}
	for _, private := range []string{`"d"`, `"p"`, `"q"`, `"dp"`, `"dq"`, `"qi"`} {
		if strings.Contains(body, private+":") {
			t.Fatalf("JWKS leaks %s", private)
		}
	}
	if !strings.Contains(body, `"kid":"k1"`) || !strings.Contains(body, `"RS256"`) {
		t.Fatalf("jwks %s", body)
	}
}

func TestKeyStoreTimeline(t *testing.T) {
	now := time.Now()
	clk := &clock{}
	ks := oidcissuer.NewKeyStore(clk.now)
	if _, err := ks.Signing(); err == nil {
		t.Fatal("empty store signed")
	}
	old := keyRecord(t, "old", now.Add(-48*time.Hour))
	cur := keyRecord(t, "cur", now.Add(-24*time.Hour))
	next := keyRecord(t, "next", now.Add(10*time.Minute))
	ra := now.Add(time.Hour)
	old.RetireAfter = &ra
	if err := ks.Update(keysJSON(t, old, cur, next)); err != nil {
		t.Fatal(err)
	}
	kids := func() []string {
		out := make([]string, 0, 3)
		for _, k := range ks.Public() {
			out = append(out, k.KeyID)
		}
		return out
	}
	sign := func() string {
		k, err := ks.Signing()
		if err != nil {
			t.Fatal(err)
		}
		return k.KeyID
	}
	if got := kids(); len(got) != 3 || sign() != "cur" {
		t.Fatalf("t0: published %v signing %s (next is published before it signs)", got, sign())
	}
	clk.shift(30 * time.Minute)
	if sign() != "next" || len(kids()) != 3 {
		t.Fatalf("t+30m: signing %s published %v", sign(), kids())
	}
	clk.shift(time.Hour)
	if got := kids(); slices.Contains(got, "old") || len(got) != 2 {
		t.Fatalf("retired key still published: %v", got)
	}
	// a bad update keeps the previous set
	for _, bad := range [][]byte{[]byte("nope"), []byte("[]"), []byte(`[{"kid":"x","pem":"junk"}]`)} {
		if ks.Update(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if sign() != "next" {
		t.Fatal("failed update replaced the key set")
	}
}

func TestConfigValidation(t *testing.T) {
	e := newEnv(t, externalURL, "")
	base := oidcissuer.Config{Issuer: externalURL, UIRedirectURI: uiRedirect, Keys: e.keys, Users: e.dir, Passwords: e.dir}
	if _, err := oidcissuer.New(base); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*oidcissuer.Config){
		"http issuer":    func(c *oidcissuer.Config) { c.Issuer = "http://ichor.example.test" },
		"issuer path":    func(c *oidcissuer.Config) { c.Issuer = externalURL + "/x" },
		"trailing slash": func(c *oidcissuer.Config) { c.Issuer = externalURL + "/" },
		"no keys":        func(c *oidcissuer.Config) { c.Keys = nil },
		"no redirect":    func(c *oidcissuer.Config) { c.UIRedirectURI = "" },
		"ttl too long":   func(c *oidcissuer.Config) { c.TokenTTL = 2 * time.Hour },
		"ttl too short":  func(c *oidcissuer.Config) { c.TokenTTL = time.Second },
		"refresh short":  func(c *oidcissuer.Config) { c.RefreshLifetime = time.Minute },
		"refresh long":   func(c *oidcissuer.Config) { c.RefreshLifetime = 90 * 24 * time.Hour },
	} {
		c := base
		mut(&c)
		if _, err := oidcissuer.New(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
