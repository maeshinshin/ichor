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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePair(t *testing.T, dir, cn string, dnsNames ...string) {
	t.Helper()

	if len(dnsNames) == 0 {
		dnsNames = []string{"localhost"}
	}

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: dnsNames,
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

// servedCN is the subject of the certificate served to a client that sends sni (none when empty).
func servedCN(addr, sni string) (string, error) {
	c, err := tls.Dial("tcp", addr, &tls.Config{ServerName: sni, InsecureSkipVerify: true}) //nolint:gosec // test: only the served subject matters
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
	if got, err := servedCN(addr, ""); err != nil || got != "operator" {
		t.Fatalf("served %q, %v; want operator", got, err)
	}
}

func TestManagedPairWhenNoOperatorFiles(t *testing.T) {
	managed, empty := t.TempDir(), t.TempDir()
	writePair(t, managed, "managed")

	addr := serve(t, servingCertificate(t.Context(), filepath.Join(empty, "tls.crt"), filepath.Join(empty, "tls.key"), managed))
	if got, err := servedCN(addr, ""); err != nil || got != "managed" {
		t.Fatalf("served %q, %v; want managed", got, err)
	}
}

// With an operator pair, a client asking for a Service name the Ichor-made certificate covers gets
// that certificate; every other handshake (an external name, an address, no SNI) gets the operator's.
func TestServiceNameSNIGetsManagedWhenOperatorPairIsSet(t *testing.T) {
	const svc = "ichor-ichor-ui-service.ichor-system.svc"

	managed, operator := t.TempDir(), t.TempDir()
	writePair(t, managed, "managed", "ichor-ichor-ui-service", svc, "ichor-ichor-ui-service.ichor-system.svc.cluster.local", "localhost")
	writePair(t, operator, "operator", "ui.example.com")

	addr := serve(t, servingCertificate(t.Context(), filepath.Join(operator, "tls.crt"), filepath.Join(operator, "tls.key"), managed))

	for _, tc := range []struct{ sni, want string }{
		{svc, "managed"},
		{"ichor-ichor-ui-service.ichor-system.svc.cluster.local", "managed"},
		{"ichor-ichor-ui-service.ichor-system.svc.", "managed"},
		{"other.ichor-system.svc", "operator"},
		{"ui.example.com", "operator"},
		{"", "operator"},
	} {
		if got, err := servedCN(addr, tc.sni); err != nil || got != tc.want {
			t.Fatalf("sni %q: served %q, %v; want %s", tc.sni, got, err, tc.want)
		}
	}
}

// Before the controller has made the Ichor-made pair, a Service name gets the operator's pair.
func TestServiceNameFallsBackToOperatorWithoutManaged(t *testing.T) {
	managed, operator := t.TempDir(), t.TempDir()
	writePair(t, operator, "operator", "ui.example.com")

	addr := serve(t, servingCertificate(t.Context(), filepath.Join(operator, "tls.crt"), filepath.Join(operator, "tls.key"), managed))
	if got, err := servedCN(addr, "ichor-ichor-ui-service.ichor-system.svc"); err != nil || got != "operator" {
		t.Fatalf("served %q, %v; want operator", got, err)
	}
}

// With no certificate the handshake fails (the HTTPS readiness probe stays
// false) and plaintext is not answered; a certificate that appears later is served.
func TestNoCertificateFailsHandshakeAndNeverPlaintext(t *testing.T) {
	dir := t.TempDir()
	addr := serve(t, servingCertificate(t.Context(), "", "", dir))

	if _, err := servedCN(addr, ""); err == nil {
		t.Fatal("handshake succeeded without a certificate")
	}

	if resp, err := http.Get("http://" + addr + "/"); err == nil && resp.StatusCode != http.StatusBadRequest {
		_ = resp.Body.Close()
		t.Fatal("plaintext HTTP was answered on the HTTPS port")
	}

	writePair(t, dir, "late")

	if got, err := servedCN(addr, ""); err != nil || got != "late" {
		t.Fatalf("served %q, %v after the certificate appeared; want late", got, err)
	}
}

func TestProbeLogFilterDropsOnlyEmptyHandshakes(t *testing.T) {
	var out bytes.Buffer

	l := log.New(probeLogFilter{&out}, "", 0)
	l.Print("http: TLS handshake error from 10.42.0.7:51234: EOF")
	l.Print("http: TLS handshake error from 10.42.0.7:51234: remote error: tls: unknown certificate")
	l.Print("http: superfluous response.WriteHeader call from example.test/x (code=200)")

	want := "http: TLS handshake error from 10.42.0.7:51234: remote error: tls: unknown certificate\n" +
		"http: superfluous response.WriteHeader call from example.test/x (code=200)\n"
	if out.String() != want {
		t.Fatalf("filtered log:\n%q\nwant:\n%q", out.String(), want)
	}
}
