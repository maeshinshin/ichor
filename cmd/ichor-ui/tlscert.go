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
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/maeshinshin/ichor/internal/certreload"
)

type getCert = func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// servingCertificate picks the main listener's certificate on every handshake
// (docs/adr/0105 decision 2): the operator's pair when its files exist, else
// the Ichor-made pair in dir. Both follow their files (internal/certreload),
// and while neither exists the handshake fails; there is no plaintext fallback.
func servingCertificate(ctx context.Context, opCert, opKey, dir string) getCert {
	managed := certreload.GetCertificate(ctx, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), certreload.Interval)
	if opCert == "" {
		return managed
	}

	operator := certreload.GetCertificate(ctx, opCert, opKey, certreload.Interval)

	return func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if fileExists(opCert) && fileExists(opKey) {
			return operator(h)
		}

		return managed(h)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}

// httpsServer is an HTTPS-only server; start it with ListenAndServeTLS("", "").
func httpsServer(addr string, h http.Handler, get getCert) *http.Server {
	return &http.Server{
		Addr: addr, Handler: h, TLSConfig: tlsConfig(get),
		// No WriteTimeout: StreamNodeLogs and the downloads run for minutes. Their request is one
		// small message, so ReadTimeout bounds a slow-body client without touching the response.
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 64 << 10,
		ErrorLog:       log.New(probeLogFilter{log.Writer()}, log.Prefix(), log.Flags()),
	}
}

// probeLogFilter drops the handshake errors of the TCP liveness probe, which connects
// and closes without sending a ClientHello, so net/http logs "TLS handshake error ...: EOF".
// Every other line goes to next unchanged.
type probeLogFilter struct{ next io.Writer }

func (f probeLogFilter) Write(p []byte) (int, error) {
	line := bytes.TrimSuffix(p, []byte("\n"))
	if bytes.Contains(line, []byte("TLS handshake error")) && bytes.HasSuffix(line, []byte(": EOF")) {
		return len(p), nil
	}

	return f.next.Write(p)
}
