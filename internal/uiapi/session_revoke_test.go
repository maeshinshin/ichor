package uiapi

import (
	"context"
	"testing"
	"time"
)

func TestAuthenticateLocalRefusesATokenIssuedBeforeNotBefore(t *testing.T) {
	s := userServer(t, true, newUser(userAlice, false, adminRole))
	ctx := context.Background()

	if err := s.setCredential(ctx, userAlice, strongPass, false); err != nil {
		t.Fatal(err)
	}

	cred, _ := readStored(t, s)
	before, after := cred.NotBefore.Add(-time.Minute), cred.NotBefore.Add(time.Second)

	a := (*Authenticator)(nil).WithLocal("https://i", fakeLocalAt{"tOld": before, "tNew": after}, s.Users.Cache)
	mk := func(tok string) string { return "Bearer e30." + b64(`{"iss":"https://i"}`) + "." + tok }

	if _, err := a.Authenticate(ctx, mk("tOld")); err == nil {
		t.Fatal("a token issued before the password change / revoke was accepted")
	}

	if _, err := a.Authenticate(ctx, mk("tNew")); err != nil {
		t.Fatalf("a token issued after notBefore: %v", err)
	}
}
