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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// cacheUILogin stores a fresh ichor-ui login for issuer in the (test) HOME.
func cacheUILogin(t *testing.T, issuer, token string) {
	t.Helper()

	dir, err := authCacheDir()
	if err != nil {
		t.Fatal(err)
	}

	e := &authCacheEntry{Issuer: issuer, ClientID: defaultAuthClient, Audience: uiAudience, IDToken: token, Expiry: time.Now().Add(time.Hour)}
	if err := saveAuthCache(dir, authCacheKey(issuer, defaultAuthClient, uiAudience), e); err != nil {
		t.Fatal(err)
	}
}

// recordAuth puts a proxy in front of the tunnel's target that records the
// Authorization header of every Connect call.
func recordAuth(t *testing.T, deps *apiDeps) func() []string {
	t.Helper()

	port, err := deps.FreePort()
	if err != nil {
		t.Fatal(err)
	}

	target, err := url.Parse(fmt.Sprintf("http://localhost:%d", port))
	if err != nil {
		t.Fatal(err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	var (
		mu   sync.Mutex
		seen []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "ichor.v1alpha1") {
			mu.Lock()
			seen = append(seen, r.Header.Get("Authorization"))
			mu.Unlock()
		}

		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	front := int32(srv.Listener.Addr().(*net.TCPAddr).Port) //nolint:errcheck,forcetypeassert // httptest listens on tcp
	deps.FreePort = func() (int32, error) { return front, nil }

	return func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), seen...)
	}
}

func TestOIDCBackendSendsTheCachedUIToken(t *testing.T) {
	setAPITestFlags(t, true)

	deps, _, stderr := oidcBackend(t, true)
	cacheUILogin(t, "https://idp", "tok123")

	seen := recordAuth(t, &deps)

	c, err := connectAPI(context.Background(), apiOpts, deps, true)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if c.Local {
		t.Fatal("a cached ichor-ui login: want the backend, not the in-process clients")
	}

	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker); err != nil {
		t.Fatal(err)
	}

	got := seen()
	if len(got) == 0 {
		t.Fatal("no Connect call reached the backend")
	}

	for _, h := range got {
		if h != "Bearer tok123" {
			t.Fatalf("Authorization = %q, want the cached token as a bearer", h)
		}
	}

	if strings.Contains(stderr.String(), "tok123") {
		t.Fatal("the token was printed")
	}
}

func TestOIDCBackendWithoutLoginSaysHowToLogIn(t *testing.T) {
	setAPITestFlags(t, true)

	deps, _, stderr := oidcBackend(t, true)

	c, err := connectAPI(context.Background(), apiOpts, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if !c.Local {
		t.Fatal("no cached login: want the in-process fallback")
	}

	if !strings.Contains(stderr.String(), "ichor auth login --issuer https://idp --audience ichor-ui") || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("want one line with the login command, got %q", stderr.String())
	}
}

func TestLogoutRemovesEveryAudienceOfTheIssuerOnly(t *testing.T) {
	dir := t.TempDir()

	put := func(issuer, aud string) string {
		k := authCacheKey(issuer, defaultAuthClient, aud)
		if err := saveAuthCache(dir, k, &authCacheEntry{Issuer: issuer, ClientID: defaultAuthClient, Audience: aud, IDToken: "x"}); err != nil {
			t.Fatal(err)
		}

		return k
	}

	kube, ui, other := put("https://i", "ichor-kube"), put("https://i/", "ichor-ui"), put("https://other", "ichor-ui")

	if n, err := deleteAuthCacheIssuer(dir, "https://i", "ichor-ui"); err != nil || n != 1 {
		t.Fatalf("one audience: %d, %v", n, err)
	}

	if e, _ := loadAuthCache(dir, ui); e != nil {
		t.Error("the ichor-ui entry survived")
	}

	if e, _ := loadAuthCache(dir, kube); e == nil {
		t.Error("an unrelated audience was removed")
	}

	put("https://i", "ichor-ui")

	if n, err := deleteAuthCacheIssuer(dir, "https://i", ""); err != nil || n != 2 {
		t.Fatalf("all audiences: %d, %v", n, err)
	}

	if e, _ := loadAuthCache(dir, other); e == nil {
		t.Error("another issuer's login was removed")
	}
}
