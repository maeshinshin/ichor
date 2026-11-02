package oidctrust

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func pemOf(t *testing.T, bits int) string {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestWeakSigningKeysAreRefused(t *testing.T) {
	if _, err := (Key{KID: "weak", PEM: pemOf(t, 1024)}).Private(); err == nil {
		t.Fatal("a 1024-bit signing key was accepted")
	}

	if _, err := (Key{KID: "ok", PEM: pemOf(t, 2048)}).Private(); err != nil {
		t.Fatalf("a 2048-bit signing key: %v", err)
	}
}
