package certreload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pair(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	must(t, err)

	kd, err := x509.MarshalECPrivateKey(k)
	must(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// publish writes the pair the way the kubelet does: a new versioned dir, then an atomic
// swap of the ..data symlink; tls.crt and tls.key are symlinks through ..data.
func publish(t *testing.T, dir, version string, c, k []byte) {
	t.Helper()

	vd := filepath.Join(dir, "..v"+version)
	must(t, os.Mkdir(vd, 0o700))
	must(t, os.WriteFile(filepath.Join(vd, "tls.crt"), c, 0o600))
	must(t, os.WriteFile(filepath.Join(vd, "tls.key"), k, 0o600))

	tmp := filepath.Join(dir, "..data_tmp")
	must(t, os.Symlink("..v"+version, tmp))
	must(t, os.Rename(tmp, filepath.Join(dir, "..data")))

	for _, n := range []string{"tls.crt", "tls.key"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err != nil {
			must(t, os.Symlink(filepath.Join("..data", n), filepath.Join(dir, n)))
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

type getter = func(*tls.ClientHelloInfo) (*tls.Certificate, error)

func cn(get getter) (string, error) {
	c, err := get(&tls.ClientHelloInfo{})
	if err != nil {
		return "", err
	}

	x, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return "", err
	}

	return x.Subject.CommonName, nil
}

func waitFor(t *testing.T, get getter, want string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := cn(get); err == nil && got == want {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("certificate %s not served within 5s", want)
}

func TestReloadKeepsOldOnInvalid(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()

	get := GetCertificate(ctx, filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), 100*time.Millisecond)
	if _, err := cn(get); err == nil {
		t.Fatal("expected an error before the files exist")
	}

	a, ak := pair(t, "A")
	publish(t, dir, "1", a, ak)
	waitFor(t, get, "A")

	b, bk := pair(t, "B")
	publish(t, dir, "2", b, bk)
	waitFor(t, get, "B")

	publish(t, dir, "3", []byte("garbage"), []byte("garbage"))
	time.Sleep(500 * time.Millisecond)

	if got, err := cn(get); err != nil || got != "B" {
		t.Fatalf("invalid files replaced the certificate: got %q, %v; want B", got, err)
	}
}
