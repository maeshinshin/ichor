package kubetrust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newCA(t *testing.T, name string) testCA {
	t.Helper()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	c, _ := x509.ParseCertificate(der)

	return testCA{c, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// server serves a leaf for "localhost" signed by a.
func (a testCA) server(t *testing.T) *httptest.Server {
	t.Helper()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "s"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}

	s := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	s.StartTLS()
	t.Cleanup(s.Close)

	return s
}

func get(t *testing.T, p *Pool, base []byte, url, hostname string) error {
	t.Helper()

	url = strings.Replace(url, "127.0.0.1", hostname, 1)
	cfg := &rest.Config{Host: url}
	cfg.CAFile = filepath.Join(t.TempDir(), "ca.crt")

	if err := os.WriteFile(cfg.CAFile, base, 0o600); err != nil {
		t.Fatal(err)
	}

	p.Apply(cfg)

	rt, err := rest.TransportFor(cfg)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := (&http.Client{Transport: rt}).Get(url)
	if err == nil {
		_ = resp.Body.Close()
	}

	return err
}

func TestPoolTrustsExtraCAAndRejectsUnknown(t *testing.T) {
	oldCA, newer, other := newCA(t, "old"), newCA(t, "new"), newCA(t, "other")
	srv := newer.server(t)
	p := &Pool{}

	if err := get(t, p, oldCA.pem, srv.URL, "localhost"); err == nil {
		t.Fatal("a server signed by an unknown CA was accepted")
	}

	if _, err := p.add(other.pem); err != nil {
		t.Fatal(err)
	}

	if err := get(t, p, oldCA.pem, srv.URL, "localhost"); err == nil {
		t.Fatal("an unrelated extra CA made the server trusted")
	}

	if _, err := p.add(newer.pem); err != nil {
		t.Fatal(err)
	}

	if err := get(t, p, oldCA.pem, srv.URL, "localhost"); err != nil {
		t.Fatalf("server signed by the extra CA rejected: %v", err)
	}

	if err := get(t, p, oldCA.pem, srv.URL, "127.0.0.1"); err == nil {
		t.Fatal("host name mismatch accepted")
	}
}

func TestBaseCAStillTrusted(t *testing.T) {
	c := newCA(t, "old")
	if err := get(t, &Pool{}, c.pem, c.server(t).URL, "localhost"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFileAtStart(t *testing.T) {
	newer := newCA(t, "new")
	path := filepath.Join(t.TempDir(), "kubernetes-ca.crt")

	if err := os.WriteFile(path, newer.pem, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pool{}
	if err := p.LoadFile(path); err != nil {
		t.Fatal(err)
	}

	if err := get(t, p, newCA(t, "old").pem, newer.server(t).URL, "localhost"); err != nil {
		t.Fatal(err)
	}

	if err := (&Pool{}).LoadFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("a missing file must not fail: %v", err)
	}
}

func TestPublishWritesOnlyCertificates(t *testing.T) {
	c := newCA(t, "new")
	keyDER, _ := x509.MarshalECPrivateKey(c.key)
	mixed := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), c.pem...)
	path := filepath.Join(t.TempDir(), "trust", "kubernetes-ca.crt")

	if err := (&Pool{}).Publish(mixed, path); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "PRIVATE KEY") || !strings.Contains(string(got), "BEGIN CERTIFICATE") {
		t.Fatalf("file must hold a certificate and no key:\n%s", got)
	}
}
