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
	"net/http"
	"strings"
	"testing"
)

// Logout revokes each stored refresh token at the
// issuer (RFC 7009) before it deletes the cache, best effort.

func putLogin(t *testing.T, f *authFlow, audience, refresh string) string {
	t.Helper()

	k := authCacheKey(f.Issuer, f.ClientID, audience)
	if err := saveAuthCache(f.Dir, k, &authCacheEntry{Issuer: f.Issuer, ClientID: f.ClientID, Audience: audience, IDToken: "x", RefreshToken: refresh}); err != nil {
		t.Fatal(err)
	}

	return k
}

func TestLogoutRevokesEveryStoredRefreshTokenThenDeletesTheCache(t *testing.T) {
	iss := newFakeIssuer(t)
	f, _ := newTestFlow(t, iss, new(int))
	kube, ui := putLogin(t, f, "ichor-kube", "r-kube"), putLogin(t, f, "ichor-ui", "r-ui")

	n, err := runAuthLogout(context.Background(), f, "")
	if err != nil || n != 2 {
		t.Fatalf("logout: %d, %v", n, err)
	}

	got := map[string]string{}

	for _, form := range iss.revoked {
		got[form.Get("token")] = form.Get("client_id") + "/" + form.Get("token_type_hint")
	}

	if len(iss.revoked) != 2 || got["r-kube"] != "ichor-cli/refresh_token" || got["r-ui"] != "ichor-cli/refresh_token" {
		t.Fatalf("revocations: %v", iss.revoked)
	}

	for _, k := range []string{kube, ui} {
		if e, _ := loadAuthCache(f.Dir, k); e != nil {
			t.Error("a login survived the logout")
		}
	}
}

func TestLogoutWithAnAudienceRevokesOnlyThatLogin(t *testing.T) {
	iss := newFakeIssuer(t)
	f, _ := newTestFlow(t, iss, new(int))
	kube := putLogin(t, f, "ichor-kube", "r-kube")
	putLogin(t, f, "ichor-ui", "r-ui")

	if n, err := runAuthLogout(context.Background(), f, "ichor-ui"); err != nil || n != 1 {
		t.Fatalf("logout: %d, %v", n, err)
	}

	if len(iss.revoked) != 1 || iss.revoked[0].Get("token") != "r-ui" {
		t.Fatalf("revocations: %v", iss.revoked)
	}

	if e, _ := loadAuthCache(f.Dir, kube); e == nil {
		t.Error("the other audience's login was deleted")
	}
}

func TestLogoutStillDeletesTheCacheWhenRevocationIsUnavailable(t *testing.T) {
	cases := map[string]func(*fakeIssuer){
		"issuer without revocation": func(i *fakeIssuer) { i.noRevocation = true },
		"revocation refused":        func(i *fakeIssuer) { i.revokeStatus = http.StatusInternalServerError },
		"issuer gone":               func(i *fakeIssuer) { i.srv.Close() },
	}

	for name, setup := range cases {
		iss := newFakeIssuer(t)
		f, stderr := newTestFlow(t, iss, new(int))
		k := putLogin(t, f, "ichor-kube", "tok-secret-1")

		setup(iss)

		if n, err := runAuthLogout(context.Background(), f, ""); err != nil || n != 1 {
			t.Fatalf("%s: %d, %v", name, n, err)
		}

		if e, _ := loadAuthCache(f.Dir, k); e != nil {
			t.Errorf("%s: the login survived", name)
		}

		if !strings.Contains(stderr.String(), "not revoked") {
			t.Errorf("%s: no warning, stderr %q", name, stderr.String())
		}

		if strings.Contains(stderr.String(), "tok-secret-1") {
			t.Errorf("%s: the refresh token was printed", name)
		}
	}
}

func TestLogoutOfALoginWithoutARefreshTokenCallsNothing(t *testing.T) {
	iss := newFakeIssuer(t)
	f, _ := newTestFlow(t, iss, new(int))
	putLogin(t, f, "ichor-kube", "")

	if n, err := runAuthLogout(context.Background(), f, ""); err != nil || n != 1 || len(iss.revoked) != 0 {
		t.Fatalf("logout: %d, %v, revocations %v", n, err, iss.revoked)
	}
}
