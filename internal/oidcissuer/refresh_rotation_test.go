package oidcissuer_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

// memStore is a RefreshStore in memory.
type memStore struct {
	mu  sync.Mutex
	cur map[string]string // family -> current jti
}

func (m *memStore) Start(_ context.Context, _, family, jti string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur[family] = jti
	return nil
}

func (m *memStore) Rotate(_ context.Context, _, family, oldJTI, newJTI string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur[family] != oldJTI {
		delete(m.cur, family)
		return oidcissuer.ErrRefreshReused
	}
	m.cur[family] = newJTI
	return nil
}

func TestRefreshTokensRotateAndReuseRevokesTheFamily(t *testing.T) {
	st := &memStore{cur: map[string]string{}}
	e := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.Refresh = st })
	first := e.tokens(clientCLI, cliRedirect, "").RefreshToken

	s, r1 := e.token(refreshForm(clientCLI, first, ""))
	if s != http.StatusOK || r1.RefreshToken == "" || r1.RefreshToken == first {
		t.Fatalf("refresh must return a new refresh token: %d %q", s, r1.RefreshToken)
	}

	s, r2 := e.token(refreshForm(clientCLI, r1.RefreshToken, ""))
	if s != http.StatusOK || r2.RefreshToken == "" {
		t.Fatalf("the rotated token must work once: %d", s)
	}

	// A thief replays the first token: refused, and the family is revoked, so the owner's
	// newest token dies as well.
	if s, _ := e.token(refreshForm(clientCLI, first, "")); s != http.StatusBadRequest {
		t.Fatalf("replayed token: %d", s)
	}

	if s, _ := e.token(refreshForm(clientCLI, r2.RefreshToken, "")); s != http.StatusBadRequest {
		t.Fatalf("newest token of a revoked family: %d", s)
	}
}

func TestRotationKeepsTheSessionExpiry(t *testing.T) {
	st := &memStore{cur: map[string]string{}}
	e := newEnvWith(t, externalURL, "", func(c *oidcissuer.Config) { c.Refresh = st })
	first := e.tokens(clientCLI, cliRedirect, "").RefreshToken
	e.clk.shift(time.Hour)

	_, r := e.token(refreshForm(clientCLI, first, ""))
	if claimsOf(t, r.RefreshToken)["exp"] != claimsOf(t, first)["exp"] || claimsOf(t, r.RefreshToken)["iat"] != claimsOf(t, first)["iat"] {
		t.Fatal("rotation must not extend the absolute session lifetime or move its start")
	}
}
