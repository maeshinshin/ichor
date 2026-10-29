package oidcissuer_test

import (
	"context"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

// TestLifetimesAreReadAtIssuance: spec.users lifetimes (ADR 0105) apply to the next token with no
// restart; an answer outside the allowed range keeps the configured value.
func TestLifetimesAreReadAtIssuance(t *testing.T) {
	e := newEnv(t, externalURL, "")
	ttl, refresh := 20*time.Minute, 48*time.Hour

	iss, err := oidcissuer.New(oidcissuer.Config{
		Issuer: externalURL, UIRedirectURI: uiRedirect, Keys: e.keys, Users: e.dir, Passwords: e.dir, Now: e.clk.now,
		Lifetimes: func(context.Context) (time.Duration, time.Duration) { return ttl, refresh },
	})
	if err != nil {
		t.Fatal(err)
	}

	e.iss, e.h = iss, iss.Handler()

	if tr := e.tokens(clientUI, uiRedirect, ""); tr.ExpiresIn != int((20 * time.Minute).Seconds()) {
		t.Fatalf("expires_in = %d, want the live 20m", tr.ExpiresIn)
	}

	ttl = 30 * time.Minute // changed while running

	if tr := e.tokens(clientUI, uiRedirect, ""); tr.ExpiresIn != int((30 * time.Minute).Seconds()) {
		t.Fatalf("expires_in = %d, want the changed 30m", tr.ExpiresIn)
	}

	ttl, refresh = 0, 0 // unset: the configured values

	if tr := e.tokens(clientUI, uiRedirect, ""); tr.ExpiresIn != 300 {
		t.Fatalf("expires_in = %d, want the configured 5m", tr.ExpiresIn)
	}
}
