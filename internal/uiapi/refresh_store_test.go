package uiapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/oidcissuer"
)

func TestRefreshStoreRotatesAndRevokesTheFamilyOnReuse(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	b := &IssuerBackend{Users: s.Users.Cache, Reader: s.Client, Writer: s.Client, Namespace: usersNS, Settings: s.Users}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)

	if err := b.Start(ctx, userAlice, "f1", "j1", exp); err != nil {
		t.Fatal(err)
	}

	if err := b.Rotate(ctx, userAlice, "f1", "j1", "j2"); err != nil {
		t.Fatalf("first rotation: %v", err)
	}

	if err := b.Rotate(ctx, userAlice, "f1", "j1", "j3"); !errors.Is(err, oidcissuer.ErrRefreshReused) {
		t.Fatalf("reused token: %v", err)
	}

	// The family is gone, so even the legitimate newest token fails now.
	if err := b.Rotate(ctx, userAlice, "f1", "j2", "j4"); !errors.Is(err, oidcissuer.ErrRefreshReused) {
		t.Fatalf("token of a revoked family: %v", err)
	}

	if cred, _ := readStored(t, s); len(cred.Families) != 0 {
		t.Fatalf("families left: %v", cred.Families)
	}
}

func TestRefreshStoreCapsFamiliesAndDropsExpired(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	b := &IssuerBackend{Users: s.Users.Cache, Reader: s.Client, Writer: s.Client, Namespace: usersNS, Settings: s.Users}
	ctx := context.Background()

	if err := b.Start(ctx, userAlice, "old", "j", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	for i := range 20 {
		if err := b.Start(ctx, userAlice, string(rune('a'+i)), "j", time.Now().Add(time.Hour+time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	cred, _ := readStored(t, s)
	if len(cred.Families) != 16 {
		t.Fatalf("%d families, want the cap of 16", len(cred.Families))
	}

	if _, ok := cred.Families["old"]; ok {
		t.Fatal("expired family kept")
	}
}

func TestRefreshStoreRevokeDeletesOnlyThatFamily(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, "viewer"))
	if err := s.setCredential(context.Background(), userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	b := &IssuerBackend{Users: s.Users.Cache, Reader: s.Client, Writer: s.Client, Namespace: usersNS, Settings: s.Users}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour)

	for _, f := range []string{"f1", "f2"} {
		if err := b.Start(ctx, userAlice, f, "j"+f, exp); err != nil {
			t.Fatal(err)
		}
	}

	if err := b.Revoke(ctx, userAlice, "f1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if err := b.Revoke(ctx, userAlice, "unknown"); err != nil {
		t.Fatalf("revoke of an unknown family: %v", err)
	}

	if err := b.Rotate(ctx, userAlice, "f1", "jf1", "x"); !errors.Is(err, oidcissuer.ErrRefreshReused) {
		t.Fatalf("token of a revoked family: %v", err)
	}

	if err := b.Rotate(ctx, userAlice, "f2", "jf2", "x"); err != nil {
		t.Fatalf("the other family must be untouched: %v", err)
	}

	if cred, _ := readStored(t, s); len(cred.Families) != 1 {
		t.Fatalf("families left: %v", cred.Families)
	}
}
