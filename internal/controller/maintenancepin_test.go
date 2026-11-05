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

package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
)

// handshake dials srv with the pin's own verification, as the maintenance client does.
func handshake(t *testing.T, p *MaintenancePin, srv *httptest.Server) error {
	t.Helper()

	conn, err := tls.Dial("tcp", srv.Listener.Addr().String(),
		&tls.Config{InsecureSkipVerify: true, VerifyConnection: p.verify}) //nolint:gosec // the test pins instead
	if err == nil {
		_ = conn.Close()
	}

	return err
}

// ADR 0108: the first configuration is only sent to the node whose maintenance certificate was pinned.
func TestMaintenancePin(t *testing.T) {
	a := httptest.NewTLSServer(http.NotFoundHandler())
	defer a.Close()

	// httptest's built-in certificate is the same for every server, so b gets its own.
	b := httptest.NewUnstartedServer(http.NotFoundHandler())
	b.TLS = &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	b.StartTLS()

	defer b.Close()

	// Trust on first use, then the same certificate again.
	tofu := &MaintenancePin{}

	if err := handshake(t, tofu, a); err != nil {
		t.Fatalf("first handshake refused: %v", err)
	}

	if err := handshake(t, tofu, a); err != nil {
		t.Fatalf("same certificate refused: %v", err)
	}

	fpA := tofu.Seen()

	// Another node answering later is refused, and the error is recognisable.
	err := handshake(t, tofu, b)
	if err == nil || !IsMaintenancePinMismatch(err) {
		t.Fatalf("a different certificate was accepted or misreported: %v", err)
	}

	// A recorded fingerprint (status.maintenanceFingerprint) is enforced from the first handshake.
	pinned, err := NewMaintenancePin(fpA)
	if err != nil {
		t.Fatal(err)
	}

	if err := handshake(t, pinned, a); err != nil {
		t.Fatalf("pinned certificate refused: %v", err)
	}

	if err := handshake(t, pinned, b); !IsMaintenancePinMismatch(err) {
		t.Fatalf("a node other than the pinned one was accepted: %v", err)
	}

	if _, err := NewMaintenancePin("not-a-fingerprint"); err == nil {
		t.Fatal("garbage fingerprint accepted")
	}
}

func selfSigned(t interface {
	Helper()
	Fatal(args ...any)
}) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1)}, &x509.Certificate{SerialNumber: big.NewInt(1)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
