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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePair(t *testing.T, dir, cn string) {
	t.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}

	kd, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}

	for name, b := range map[string]*pem.Block{
		"tls.crt": {Type: "CERTIFICATE", Bytes: der}, "tls.key": {Type: "EC PRIVATE KEY", Bytes: kd},
	} {
		if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(b), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// serve starts an HTTPS-only server on a free port and returns its address.
func serve(t *testing.T, get getCert) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := httpsServer("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }), get)

	go func() { _ = srv.ServeTLS(l, "", "") }()

	t.Cleanup(func() { _ = srv.Close() })

	return l.Addr().String()
}

func servedCN(addr string) (string, error) {
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test: only the served subject matters
	if err != nil {
		return "", err
	}

	defer func() { _ = c.Close() }()

	return c.ConnectionState().PeerCertificates[0].Subject.CommonName, nil
}

func TestOperatorPairBeatsManaged(t *testing.T) {
	managed, operator := t.TempDir(), t.TempDir()
	writePair(t, managed, "managed")
	writePair(t, operator, "operator")

	addr := serve(t, servingCertificate(t.Context(), filepath.Join(operator, "tls.crt"), filepath.Join(operator, "tls.key"), managed))
	if got, err := servedCN(addr); err != nil || got != "operator" {
		t.Fatalf("served %q, %v; want operator", got, err)
	}
}

func TestManagedPairWhenNoOperatorFiles(t *testing.T) {
	managed, empty := t.TempDir(), t.TempDir()
	writePair(t, managed, "managed")

	addr := serve(t, servingCertificate(t.Context(), filepath.Join(empty, "tls.crt"), filepath.Join(empty, "tls.key"), managed))
	if got, err := servedCN(addr); err != nil || got != "managed" {
		t.Fatalf("served %q, %v; want managed", got, err)
	}
}

// With no certificate the handshake fails (the HTTPS readiness probe stays
// false) and plaintext is not answered; a certificate that appears later is served.
func TestNoCertificateFailsHandshakeAndNeverPlaintext(t *testing.T) {
	dir := t.TempDir()
	addr := serve(t, servingCertificate(t.Context(), "", "", dir))

	if _, err := servedCN(addr); err == nil {
		t.Fatal("handshake succeeded without a certificate")
	}

	if resp, err := http.Get("http://" + addr + "/"); err == nil && resp.StatusCode != http.StatusBadRequest {
		_ = resp.Body.Close()
		t.Fatal("plaintext HTTP was answered on the HTTPS port")
	}

	writePair(t, dir, "late")

	if got, err := servedCN(addr); err != nil || got != "late" {
		t.Fatalf("served %q, %v after the certificate appeared; want late", got, err)
	}
}
