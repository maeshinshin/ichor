package oidcissuer_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

const (
	formType           = "application/x-www-form-urlencoded"
	pToken             = "token"
	codeInvalidGrant   = "invalid_grant"
	codeInvalidRequest = "invalid_request"
)

// RFC 7009: POST /oidc/revoke, form token (+ optional token_type_hint) and client_id for a public client.
func revokeForm(client, token string) url.Values {
	return url.Values{pClientID: {client}, pToken: {token}, "token_type_hint": {pRefresh}}
}

func (e *env) revokeRaw(ctype, body string) (int, string) {
	rec := e.do(e.h, http.MethodPost, "/oidc/revoke", ctype, body)
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Error
}

func (e *env) revoke(form url.Values) (int, string) { return e.revokeRaw(formType, form.Encode()) }

func newRevokeEnv(t *testing.T) *env {
	t.Helper()
	st := &memStore{cur: map[string]string{}}
	return newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.Refresh = st })
}

func TestRevokeEndsTheFamilySoNeitherTheTokenNorItsPredecessorRefreshes(t *testing.T) {
	for _, client := range []struct{ id, redirect string }{{clientUI, uiRedirect}, {clientCLI, cliRedirect}} {
		e := newRevokeEnv(t)
		first := e.tokens(client.id, client.redirect, "").RefreshToken
		_, r1 := e.token(refreshForm(client.id, first, ""))

		// The newest token of the family is the one the signing-out client holds.
		if s, errc := e.revoke(revokeForm(client.id, r1.RefreshToken)); s != http.StatusOK || errc != "" {
			t.Fatalf("%s revoke: %d %q", client.id, s, errc)
		}
		if s, _ := e.token(refreshForm(client.id, r1.RefreshToken, "")); s != http.StatusBadRequest {
			t.Fatalf("%s: the revoked token still refreshes: %d", client.id, s)
		}
		if s, _ := e.token(refreshForm(client.id, first, "")); s != http.StatusBadRequest {
			t.Fatalf("%s: an earlier token of the family still refreshes: %d", client.id, s)
		}
	}
}

func TestRevokeOfARetiredTokenEndsTheFamilyToo(t *testing.T) {
	e := newRevokeEnv(t)
	first := e.tokens(clientCLI, cliRedirect, "").RefreshToken
	_, r1 := e.token(refreshForm(clientCLI, first, ""))

	if s, _ := e.revoke(revokeForm(clientCLI, first)); s != http.StatusOK {
		t.Fatalf("revoke of a retired token: %d", s)
	}
	if s, _ := e.token(refreshForm(clientCLI, r1.RefreshToken, "")); s != http.StatusBadRequest {
		t.Fatalf("the family survived: %d", s)
	}
}

func TestRevokeAnswers200ForATokenThatIsNotValid(t *testing.T) {
	e := newRevokeEnv(t)
	live := e.tokens(clientCLI, cliRedirect, "")

	for name, tok := range map[string]string{"garbage": "not-a-jwt", "an ID token": live.IDToken} {
		if s, errc := e.revoke(revokeForm(clientCLI, tok)); s != http.StatusOK || errc != "" {
			t.Errorf("%s: %d %q", name, s, errc)
		}
	}
	if s, _ := e.token(refreshForm(clientCLI, live.RefreshToken, "")); s != http.StatusOK {
		t.Fatalf("revoking junk touched a live family: %d", s)
	}

	// An expired token answers 200 too: there is nothing left to revoke.
	e.clk.shift(13 * time.Hour)

	if s, _ := e.revoke(revokeForm(clientCLI, live.RefreshToken)); s != http.StatusOK {
		t.Fatalf("expired token: %d", s)
	}
}

func TestRevokeRefusesATokenIssuedToAnotherClientAndKeepsTheFamily(t *testing.T) {
	e := newRevokeEnv(t)
	rt := e.tokens(clientCLI, cliRedirect, "").RefreshToken

	if s, errc := e.revoke(revokeForm(clientUI, rt)); s != http.StatusBadRequest || errc != codeInvalidGrant {
		t.Fatalf("other client's token: %d %q", s, errc)
	}
	if s, _ := e.token(refreshForm(clientCLI, rt, "")); s != http.StatusOK {
		t.Fatalf("a refused revoke ended the family: %d", s)
	}
}

func TestRevokeRejectsMalformedRequests(t *testing.T) {
	e := newRevokeEnv(t)
	rt := e.tokens(clientCLI, cliRedirect, "").RefreshToken

	for name, c := range map[string]struct {
		ctype, body string
		status      int
		errc        string
	}{
		"no token":          {formType, url.Values{pClientID: {clientCLI}}.Encode(), 400, codeInvalidRequest},
		"empty token":       {formType, url.Values{pClientID: {clientCLI}, pToken: {""}}.Encode(), 400, codeInvalidRequest},
		"json body":         {"application/json", `{"token":"x"}`, 400, codeInvalidRequest},
		"repeated token":    {formType, "client_id=ichor-cli&token=a&token=b", 400, codeInvalidRequest},
		"unknown client":    {formType, url.Values{pClientID: {"evil"}, pToken: {rt}}.Encode(), 401, "invalid_client"},
		"no client":         {formType, url.Values{pToken: {rt}}.Encode(), 401, "invalid_client"},
		"access_token hint": {formType, url.Values{pClientID: {clientCLI}, pToken: {rt}, "token_type_hint": {"access_token"}}.Encode(), 400, "unsupported_token_type"},
	} {
		if s, errc := e.revokeRaw(c.ctype, c.body); s != c.status || errc != c.errc {
			t.Errorf("%s: %d %q, want %d %q", name, s, errc, c.status, c.errc)
		}
	}

	if s, _ := e.token(refreshForm(clientCLI, rt, "")); s != http.StatusOK {
		t.Fatalf("a rejected revoke ended the family: %d", s)
	}

	if rec := e.do(e.h, http.MethodGet, "/oidc/revoke", "", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /oidc/revoke: %d", rec.Code)
	}
}

func TestRevokeIsAcceptedWithoutARefreshStore(t *testing.T) {
	e := newEnv(t, externalURL, "")
	rt := e.tokens(clientCLI, cliRedirect, "").RefreshToken

	if s, _ := e.revoke(revokeForm(clientCLI, rt)); s != http.StatusOK {
		t.Fatalf("stateless issuer: %d", s)
	}
}

type failingRevoke struct{ memStore }

func (*failingRevoke) Revoke(context.Context, string, string) error { return errors.New("etcd down") }

func TestRevokeSaysServerErrorWhenTheStoreFails(t *testing.T) {
	st := &failingRevoke{memStore{cur: map[string]string{}}}
	e := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.Refresh = st })
	rt := e.tokens(clientCLI, cliRedirect, "").RefreshToken

	if s, errc := e.revoke(revokeForm(clientCLI, rt)); s != http.StatusInternalServerError || errc != "server_error" {
		t.Fatalf("store failure: %d %q", s, errc)
	}
}

func TestRevokeIsRateLimitedPerSource(t *testing.T) {
	e := newRevokeEnv(t)

	for range 300 {
		if s, _ := e.revoke(revokeForm(clientCLI, "x")); s == http.StatusTooManyRequests {
			return
		}
	}

	t.Fatal("/oidc/revoke has no rate limit")
}

func TestDiscoveryAdvertisesTheRevocationEndpointOnTheExternalListenerOnly(t *testing.T) {
	e := newRevokeEnv(t)
	rec := e.do(e.h, http.MethodGet, "/.well-known/openid-configuration", "", "")

	var doc map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc["revocation_endpoint"] != externalURL+"/oidc/revoke" {
		t.Fatalf("revocation_endpoint = %v", doc["revocation_endpoint"])
	}

	if rec := e.do(e.iss.InternalHandler(), http.MethodPost, "/oidc/revoke", formType, "token=x"); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("internal handler serves /oidc/revoke: %d", rec.Code)
	}
}
