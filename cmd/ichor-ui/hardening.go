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
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// Request size limits. connect-go reads and decodes a request
// message before any interceptor runs, so the limit has to be on the handler
// or an unauthenticated caller can make the pod allocate without bound. The
// limit applies to the decompressed message, so a gzip bomb is covered too.
// Every RPC takes one small request message (the streaming ones are server
// streams); only the services that carry pasted documents get more.
const (
	rpcMaxBytes       = 256 << 10
	rpcMaxBytesUpload = 1 << 20 // reclaim's talosconfig, patch documents, Cilium values
	// bodyBackstop caps any body on the main mux, RPC or not.
	bodyBackstop = 2 << 20
)

// mountRPC registers every Connect service on mux behind the metrics, access-log and auth interceptors.
func mountRPC(mux *http.ServeMux, s *uiapi.Server, authn *uiapi.Authenticator, allowAnonymous bool) {
	opts := func(max int) connect.HandlerOption {
		return connect.WithHandlerOptions(
			connect.WithReadMaxBytes(max),
			connect.WithInterceptors(uiapi.SanitizeErrors(log.Default()), uiapi.MetricsInterceptor(), uiapi.AccessLogInterceptor(log.Default(), false), uiapi.AuthInterceptor(authn, allowAnonymous)),
		)
	}
	small, big := opts(rpcMaxBytes), opts(rpcMaxBytesUpload)

	mux.Handle(ichorv1alpha1connect.NewClusterServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewMachineServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewNodeServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewPatchServiceHandler(s, big))
	mux.Handle(ichorv1alpha1connect.NewOperationServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewDiagnosticsServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewUserServiceHandler(s, small))
	mux.Handle(ichorv1alpha1connect.NewCniServiceHandler(s, big))
	mux.Handle(ichorv1alpha1connect.NewReclaimServiceHandler(s, big))
	mux.Handle(ichorv1alpha1connect.NewSettingsServiceHandler(s, small))
}

// contentSecurityPolicy allows exactly what the Vite build needs: scripts and connections from
// the page's own origin, and no inline script (the theme snippet is a file,
// ui/public/theme-init.js). style-src keeps 'unsafe-inline' because two stock
// pieces inject a <style> element at runtime (ui/src/lib/radix/select.tsx and
// the sonner shim); inline CSS cannot run script, so the risk is accepted
// rather than editing stock components. With an external OIDC provider the SPA itself
// fetches the provider's discovery document and token endpoint, whose origins are not
// known here, so connect-src widens to any https origin; scripts stay 'self' only.
func contentSecurityPolicy(externalOIDC bool) string {
	connect := "'self'"
	if externalOIDC {
		connect = "'self' https:"
	}

	return "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src " + connect + "; " +
		"img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// securityHeaders sets the browser-facing protections on every response of the
// listener, the issuer's login pages included.
func securityHeaders(next http.Handler, csp string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		// The listener is HTTPS-only.
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// tlsConfig is the TLS policy of both listeners: TLS 1.2 or newer, 1.2 only
// with ECDHE and an AEAD cipher, and the two curves every client supports.
func tlsConfig(get getCert) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
		GetCertificate:   get,
	}
}

// newInternalHTTPServer is the kube-apiserver-only listener: tiny discovery and
// JWKS documents, so every deadline is short.
func newInternalHTTPServer(addr string, h http.Handler, get getCert) *http.Server {
	return &http.Server{
		Addr: addr, Handler: securityHeaders(http.MaxBytesHandler(h, 4<<10), contentSecurityPolicy(false)), TLSConfig: tlsConfig(get),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
}
