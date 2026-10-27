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
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"time"

	"github.com/maeshinshin/ichor/internal/oidctrust"
)

const (
	issuerCALifetime   = 10 * 365 * 24 * time.Hour
	issuerLeafLifetime = 365 * 24 * time.Hour

	// issuerCARenewBefore is how long before the CA expires a new one is made. The CA is in
	// machine config, so replacing it is a control-plane rollout; ten years leaves ample warning.
	issuerCARenewBefore = 90 * 24 * time.Hour

	issuerSigningKeyBits = 3072
	issuerCABits         = 3072
	issuerLeafBits       = 2048

	// issuerClockSkew backdates NotBefore so a node with a slow clock still accepts the cert.
	issuerClockSkew = 5 * time.Minute
)

// keyPair is a certificate with its private key, both PEM.
type keyPair struct {
	certPEM, keyPEM []byte
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func encodeKey(k *rsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, fmt.Errorf("encoding private key: %w", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func decodeCert(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("certificate is not PEM")
	}

	return x509.ParseCertificate(block.Bytes)
}

func decodeKey(keyPEM []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("private key is not PKCS#8")
	}

	k, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}

	return k, nil
}

// newCA makes the Ichor-made CA (RSA, 10 years).
func newCA(now time.Time) (keyPair, error) {
	key, err := rsa.GenerateKey(rand.Reader, issuerCABits)
	if err != nil {
		return keyPair{}, fmt.Errorf("generating CA key: %w", err)
	}

	serial, err := newSerial()
	if err != nil {
		return keyPair{}, err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ichor-oidc-ca"},
		NotBefore:             now.Add(-issuerClockSkew),
		NotAfter:              now.Add(issuerCALifetime),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return keyPair{}, fmt.Errorf("signing CA: %w", err)
	}

	keyPEM, err := encodeKey(key)
	if err != nil {
		return keyPair{}, err
	}

	return keyPair{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM}, nil
}

// caUsable reports whether the stored CA parses and has more than issuerCARenewBefore left.
func caUsable(ca keyPair, now time.Time) (*x509.Certificate, *rsa.PrivateKey, bool) {
	cert, err := decodeCert(ca.certPEM)
	if err != nil || !cert.IsCA {
		return nil, nil, false
	}

	key, err := decodeKey(ca.keyPEM)
	if err != nil {
		return nil, nil, false
	}

	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) || now.Add(issuerCARenewBefore).After(cert.NotAfter) {
		return nil, nil, false
	}

	return cert, key, true
}

// leafSpec is what the leaf must cover.
type leafSpec struct {
	ip  net.IP
	dns []string
}

// leafUsable reports whether the stored leaf is signed by ca, matches its key and spec, and is
// still before two thirds of its lifetime (the renewal point).
func leafUsable(leaf keyPair, ca *x509.Certificate, spec leafSpec, now time.Time) bool {
	cert, err := decodeCert(leaf.certPEM)
	if err != nil {
		return false
	}

	key, err := decodeKey(leaf.keyPEM)
	if err != nil {
		return false
	}

	if pub, ok := cert.PublicKey.(*rsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return false
	}

	if cert.CheckSignatureFrom(ca) != nil {
		return false
	}

	if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(spec.ip) ||
		!slices.Equal(cert.DNSNames, spec.dns) {
		return false
	}

	renewAt := cert.NotBefore.Add(cert.NotAfter.Sub(cert.NotBefore) * 2 / 3)

	return now.Before(renewAt)
}

// newLeaf signs a one-year serving certificate for the internal listener.
func newLeaf(ca *x509.Certificate, caKey *rsa.PrivateKey, spec leafSpec, now time.Time) (keyPair, error) {
	key, err := rsa.GenerateKey(rand.Reader, issuerLeafBits)
	if err != nil {
		return keyPair{}, fmt.Errorf("generating leaf key: %w", err)
	}

	serial, err := newSerial()
	if err != nil {
		return keyPair{}, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ichor-ui"},
		NotBefore:    now.Add(-issuerClockSkew),
		NotAfter:     now.Add(issuerLeafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{spec.ip},
		DNSNames:     spec.dns,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return keyPair{}, fmt.Errorf("signing leaf: %w", err)
	}

	keyPEM, err := encodeKey(key)
	if err != nil {
		return keyPair{}, err
	}

	return keyPair{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM}, nil
}

// newSigningKey makes one RS256 key with an id derived from its public half.
func newSigningKey(now, activeFrom time.Time) (oidctrust.Key, error) {
	key, err := rsa.GenerateKey(rand.Reader, issuerSigningKeyBits)
	if err != nil {
		return oidctrust.Key{}, fmt.Errorf("generating signing key: %w", err)
	}

	keyPEM, err := encodeKey(key)
	if err != nil {
		return oidctrust.Key{}, err
	}

	sum := sha256.Sum256(x509.MarshalPKCS1PublicKey(&key.PublicKey))

	return oidctrust.Key{
		KID: hex.EncodeToString(sum[:8]), PEM: string(keyPEM),
		CreatedAt: now.UTC(), ActiveFrom: activeFrom.UTC(),
	}, nil
}

// rotationPlan is the key schedule.
type rotationPlan struct {
	// RotateEvery is how often a new key is added.
	RotateEvery time.Duration
	// ActivationDelay is how long a new key is published before it signs.
	ActivationDelay time.Duration
	// TokenLife is the longest-lived token the issuer signs (the refresh lifetime).
	TokenLife time.Duration
	// RetireGrace is the margin added after the last token a retired key signed can have expired.
	RetireGrace time.Duration
}

// rotateKeys advances the key set to now. It returns the new set, whether it differs from the
// input, and the next moment something is due. An empty input gets a first key that signs at once.
func rotateKeys(in oidctrust.Keys, now time.Time, plan rotationPlan) (oidctrust.Keys, bool, time.Time, error) {
	out := oidctrust.Keys{Version: oidctrust.KeysVersion}
	changed := false

	for _, k := range in.Keys {
		if k.RetireAfter != nil && !k.RetireAfter.After(now) {
			changed = true

			continue
		}

		out.Keys = append(out.Keys, k)
	}

	newest := -1

	for i, k := range out.Keys {
		if newest < 0 || k.CreatedAt.After(out.Keys[newest].CreatedAt) {
			newest = i
		}
	}

	switch {
	case newest < 0:
		k, err := newSigningKey(now, now)
		if err != nil {
			return in, false, time.Time{}, err
		}

		out.Keys, changed = append(out.Keys, k), true
	case !out.Keys[newest].CreatedAt.Add(plan.RotateEvery).After(now):
		activeFrom := now.Add(plan.ActivationDelay)

		k, err := newSigningKey(now, activeFrom)
		if err != nil {
			return in, false, time.Time{}, err
		}

		// Every key signing today stops when the successor starts; its last token is
		// TokenLife later, plus the grace.
		retire := activeFrom.Add(plan.TokenLife + plan.RetireGrace).UTC()

		for i := range out.Keys {
			if out.Keys[i].RetireAfter == nil {
				r := retire
				out.Keys[i].RetireAfter = &r
			}
		}

		out.Keys, changed = append(out.Keys, k), true
	}

	// A key that stops signing without a retire time (a successor appeared by other means) gets
	// one, so it cannot be kept forever.
	if signing, ok := out.Signing(now); ok {
		for i := range out.Keys {
			k := &out.Keys[i]
			if k.KID != signing.KID && k.RetireAfter == nil && k.ActiveFrom.Before(signing.ActiveFrom) {
				r := signing.ActiveFrom.Add(plan.TokenLife + plan.RetireGrace).UTC()
				k.RetireAfter, changed = &r, true
			}
		}
	}

	return out, changed, nextKeyEvent(out, now, plan), nil
}

// nextKeyEvent is the earliest of: a pending key activating, a key retiring, the next rotation.
func nextKeyEvent(ks oidctrust.Keys, now time.Time, plan rotationPlan) time.Time {
	var next time.Time

	consider := func(t time.Time) {
		if t.After(now) && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}

	var newest time.Time

	for _, k := range ks.Keys {
		consider(k.ActiveFrom)

		if k.RetireAfter != nil {
			consider(*k.RetireAfter)
		}

		if k.CreatedAt.After(newest) {
			newest = k.CreatedAt
		}
	}

	consider(newest.Add(plan.RotateEvery))

	return next
}
