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

package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/maeshinshin/ichor/internal/uiapi"
)

func TestRPCBodyLimit(t *testing.T) {
	mux := http.NewServeMux()
	mountRPC(mux, &uiapi.Server{}, nil, true)

	post := func(path string, n int) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"x":"`+strings.Repeat("a", n)+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		return rec.Code
	}

	// 300 KiB is over the default limit; reclaim (a pasted talosconfig) allows more.
	if code := post("/ichor.v1alpha1.ClusterService/ListClusters", 300<<10); code != http.StatusTooManyRequests {
		t.Fatalf("oversized default RPC body = %d, want 429 (resource exhausted)", code)
	}

	if code := post("/ichor.v1alpha1.ReclaimService/CreateReclaim", 300<<10); code == http.StatusTooManyRequests {
		t.Fatalf("300 KiB reclaim body was refused; the upload RPC needs a larger limit")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), contentSecurityPolicy(false))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Strict-Transport-Security":  "max-age=31536000",
		"X-Frame-Options":            "DENY",
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Permissions-Policy":         "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, d := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, d) {
			t.Errorf("CSP %q lacks %q", csp, d)
		}
	}

	if strings.Contains(csp, "script-src 'self' 'unsafe") {
		t.Errorf("CSP allows inline script: %q", csp)
	}
}

func TestExternalOIDCWidensConnectSrcOnly(t *testing.T) {
	if got := contentSecurityPolicy(true); !strings.Contains(got, "connect-src 'self' https:;") || !strings.Contains(got, "script-src 'self';") {
		t.Fatalf("csp = %q", got)
	}
}

func TestServerLimits(t *testing.T) {
	get := func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return nil, nil }
	srv := httpsServer(":0", http.NewServeMux(), get)

	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Fatalf("main listener lacks a limit: %+v", srv)
	}

	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout %v would cut streaming RPCs and downloads", srv.WriteTimeout)
	}

	assertTLS(t, srv.TLSConfig)
}

func assertTLS(t *testing.T, c *tls.Config) {
	t.Helper()

	if c.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion %x", c.MinVersion)
	}

	for _, id := range c.CipherSuites {
		for _, s := range tls.CipherSuites() {
			if s.ID == id && (s.Insecure || strings.Contains(s.Name, "CBC") || strings.HasPrefix(s.Name, "TLS_RSA_WITH")) {
				t.Errorf("cipher suite %s is not AEAD ECDHE", s.Name)
			}
		}
	}

	if len(c.CipherSuites) == 0 || len(c.CurvePreferences) != 2 {
		t.Errorf("cipher suites / curves unpinned: %v %v", c.CipherSuites, c.CurvePreferences)
	}
}

func TestMetricsNeedAKubernetesToken(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })

	h, err := authenticatedMetrics(&rest.Config{Host: "https://127.0.0.1:1"}, inner)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("an anonymous /metrics scrape got %d (handler called: %v)", rec.Code, called)
	}
}

func TestInternalServerLimits(t *testing.T) {
	srv := newInternalHTTPServer(":0", http.NewServeMux(), nil)

	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 || srv.ReadHeaderTimeout == 0 {
		t.Fatalf("internal listener lacks a limit: %+v", srv)
	}

	if srv.ReadTimeout > time.Minute {
		t.Fatalf("ReadTimeout %v too long for kube-apiserver-only listener", srv.ReadTimeout)
	}

	assertTLS(t, srv.TLSConfig)
}
