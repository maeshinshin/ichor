package oidcissuer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

// loginFrom posts a login as if from remote (and with an X-Forwarded-For when xff is set).
func (e *env) loginFrom(remote, xff, ticket, password string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{pTicket: ticket, pUsername: userAlice, pPassword: password})
	req := httptest.NewRequest(http.MethodPost, "/oidc/login", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestFailuresOfOneSourceDoNotLockTheUserOutForOthers(t *testing.T) {
	e := newEnv(t, externalURL, "")
	ticket := e.ticket(e.authorize(clientUI, uiRedirect, "", newPKCE()))

	for range 6 {
		e.loginFrom("198.51.100.7:1111", "", ticket, "wrong")
	}
	if rec := e.loginFrom("198.51.100.7:1112", "", ticket, "correct horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the attacking source is not slowed: %d", rec.Code)
	}
	if rec := e.loginFrom("203.0.113.9:2222", "", ticket, "correct horse"); rec.Code != http.StatusOK {
		t.Fatalf("the real user from another source is locked out: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUserCeilingSlowsButNeverRefuses(t *testing.T) {
	e := newEnv(t, externalURL, "")
	ticket := e.ticket(e.authorize(clientUI, uiRedirect, "", newPKCE()))

	// Failures from many sources, each below its own per-source limit.
	for n := range 31 {
		e.loginFrom("198.51.100."+strconv.Itoa(n+1)+":1", "", ticket, "wrong")
	}

	start := time.Now()
	rec := e.loginFrom("203.0.113.9:2222", "", ticket, "correct horse")
	if rec.Code != http.StatusOK {
		t.Fatalf("the real user is refused under a distributed guessing attack: %d", rec.Code)
	}
	if time.Since(start) < time.Second {
		t.Fatalf("no delay past the per-user ceiling: %v", time.Since(start))
	}
}

func TestTicketBuysOneSuccessfulLogin(t *testing.T) {
	e := newEnv(t, externalURL, "")
	ticket := e.ticket(e.authorize(clientUI, uiRedirect, "", newPKCE()))

	if rec := e.login(ticket, userAlice, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	if rec := e.login(ticket, userAlice, "correct horse"); rec.Code != http.StatusOK {
		t.Fatalf("retry with the same ticket after a typo: %d", rec.Code)
	}
	if rec := e.login(ticket, userAlice, "correct horse"); rec.Code != http.StatusBadRequest {
		t.Fatalf("a ticket was used for a second login: %d", rec.Code)
	}
}

func TestAuthorizeAndTokenAreRateLimitedPerSource(t *testing.T) {
	e := newEnv(t, externalURL, "")

	limited := false
	for range 300 {
		if e.authorize(clientUI, uiRedirect, "", newPKCE()).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("/oidc/authorize has no rate limit")
	}

	limited = false
	for range 300 {
		if s, _ := e.token(refreshForm(clientCLI, "x", "")); s == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("/oidc/token has no rate limit")
	}
}

func TestForwardedForOnlyCountsFromATrustedProxy(t *testing.T) {
	proxy := netip.MustParsePrefix("10.0.0.0/8")
	e := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.TrustedProxies = []netip.Prefix{proxy} })
	ticket := e.ticket(e.authorize(clientUI, uiRedirect, "", newPKCE()))

	// An attacker behind the proxy fails six times; a user behind the same proxy is not affected.
	for range 6 {
		e.loginFrom("10.0.0.5:1", "198.51.100.7", ticket, "wrong")
	}
	if rec := e.loginFrom("10.0.0.5:2", "198.51.100.7", ticket, "correct horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attacker not slowed: %d", rec.Code)
	}
	if rec := e.loginFrom("10.0.0.5:3", "203.0.113.9", ticket, "correct horse"); rec.Code != http.StatusOK {
		t.Fatalf("another client behind the proxy is slowed: %d", rec.Code)
	}

	// A header from an untrusted peer is ignored: one source whatever it claims.
	e2 := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.TrustedProxies = []netip.Prefix{proxy} })
	t2 := e2.ticket(e2.authorize(clientUI, uiRedirect, "", newPKCE()))
	for n := range 6 {
		e2.loginFrom("192.0.2.50:1", "198.51.100."+strconv.Itoa(n+1), t2, "wrong")
	}
	if rec := e2.loginFrom("192.0.2.50:1", "198.51.100.99", t2, "correct horse"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a spoofed X-Forwarded-For from an untrusted peer reset the counter: %d", rec.Code)
	}
}
