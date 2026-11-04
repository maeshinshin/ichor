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
	stdx509 "crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

type testCA struct {
	cert *stdx509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &stdx509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: stdx509.KeyUsageCertSign,
	}

	der, err := stdx509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, _ := stdx509.ParseCertificate(der)

	return testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca testCA) issue(t *testing.T) []byte {
	t.Helper()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &stdx509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1), Subject: pkix.Name{CommonName: "etcd"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}

	der, err := stdx509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// Two CAs with the same subject name must still be told apart: the rotation's old and new etcd CAs
// are generated the same way and carry the same name.
func TestEtcdPKIFromTellsTwoCAsOfTheSameNameApart(t *testing.T) {
	oldCA, newCA := newTestCA(t, "etcd"), newTestCA(t, "etcd")
	bundle := slices.Concat(newCA.pem, oldCA.pem)

	got, err := etcdPKIFrom(bundle, oldCA.issue(t))
	if err != nil {
		t.Fatal(err)
	}

	oldFP, _ := secretsbundle.Fingerprint(oldCA.pem)
	newFP, _ := secretsbundle.Fingerprint(newCA.pem)

	if !slices.Equal(got.CAs, []string{newFP, oldFP}) {
		t.Fatalf("the trusted CAs must be listed in the order of the field, got %v", got.CAs)
	}

	if got.ServerSignedBy != oldFP {
		t.Fatalf("the server certificate was signed by the old CA, got %q", got.ServerSignedBy)
	}

	got, err = etcdPKIFrom(bundle, newCA.issue(t))
	if err != nil || got.ServerSignedBy != newFP {
		t.Fatalf("the server certificate was signed by the new CA, got %q %v", got.ServerSignedBy, err)
	}
}

func TestEtcdPKIFromRefusesWhatItCannotRead(t *testing.T) {
	ca := newTestCA(t, "etcd")

	if _, err := etcdPKIFrom(nil, ca.issue(t)); err == nil {
		t.Fatal("an empty CA field must be an error")
	}

	if _, err := etcdPKIFrom(ca.pem, []byte("nope")); err == nil {
		t.Fatal("an unreadable server certificate must be an error")
	}

	got, err := etcdPKIFrom(ca.pem, newTestCA(t, "other").issue(t))
	if err != nil || got.ServerSignedBy != "" {
		t.Fatalf("a server certificate signed by none of the CAs reports no signer, got %q %v", got.ServerSignedBy, err)
	}
}
