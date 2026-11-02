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

// Package oidctrust holds the names and shapes of the objects the controller keeps for ichor-ui's
// own OIDC issuer (ADR 0094, Decisions 2 and 3): the signing-key Secret, the internal CA and leaf
// TLS Secrets and the public trust ConfigMap. The controller writes them; ichor-ui and the
// renderer only read them, through this package, so the two sides cannot drift.
package oidctrust

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Object names, all in the controller's namespace (ichor-system).
const (
	// SigningSecret holds the issuer's signing keys under KeysKey.
	SigningSecret = "ichor-oidc-signing"
	// CASecret holds the internal CA (tls.crt, tls.key).
	CASecret = "ichor-oidc-ca"
	// TLSSecret holds the leaf for ichor-ui's internal HTTPS listener (tls.crt, tls.key, ca.crt).
	TLSSecret = "ichor-oidc-tls"
	// TrustConfigMap carries the public parts a render reads: DiscoveryURLKey and CAKey.
	TrustConfigMap = "ichor-oidc-trust"
	// UICAName names both the ConfigMap and the Secret that publish the serving CA's public part
	// (key CAKey) for an edge to verify ichor-ui with (ADR 0105).
	UICAName = "ichor-ui-ca"
	// CredentialsSecret is the argon2id hash Secret (ADR 0094 Decision 1); key = user name.
	CredentialsSecret = "ichor-user-credentials"
)

// Keys inside those objects.
const (
	KeysKey         = "keys.json"
	CertKey         = "tls.crt"
	PrivateKeyKey   = "tls.key"
	CAKey           = "ca.crt"
	DiscoveryURLKey = "discoveryURL"
)

// The label every install path (kustomize, chart) puts on the ichor-ui Deployment and Service, so
// nothing finds them by name: kustomize prefixes names.
const (
	UIComponentLabel = "app.kubernetes.io/component"
	UIComponentValue = "ui"
)

// InternalPort is the port of ichor-ui's HTTPS listener that serves discovery and the JWKS.
const InternalPort = 8444

// MinKeyBits is the smallest RSA signing key ParseKeys and Private accept (docs/adr/0108).
const MinKeyBits = 2048

// KeysVersion is the version of the keys.json document.
const KeysVersion = 1

// Key is one RS256 signing key. Private is a PKCS#8 PEM.
type Key struct {
	KID string `json:"kid"`
	PEM string `json:"pem"`
	// CreatedAt is when the controller made the key.
	CreatedAt time.Time `json:"createdAt"`
	// ActiveFrom is when the key may be used to sign. Before it the key is only published, so
	// every ichor-ui replica has it loaded before any token carries its kid.
	ActiveFrom time.Time `json:"activeFrom"`
	// RetireAfter is when the key is dropped. It is nil while the key is the signing key; the
	// controller sets it when a successor takes over.
	RetireAfter *time.Time `json:"retireAfter,omitempty"`
}

// Keys is the content of the signing Secret's KeysKey.
type Keys struct {
	Version int   `json:"v"`
	Keys    []Key `json:"keys"`
}

// ParseKeys decodes and validates keys.json; error text never contains key material.
func ParseKeys(raw []byte) (Keys, error) {
	var k Keys
	if err := json.Unmarshal(raw, &k); err != nil {
		return Keys{}, errors.New("signing keys document is not valid JSON")
	}

	if k.Version != KeysVersion {
		return Keys{}, fmt.Errorf("signing keys document has unsupported version %d", k.Version)
	}

	seen := map[string]bool{}

	for i := range k.Keys {
		if k.Keys[i].KID == "" || seen[k.Keys[i].KID] {
			return Keys{}, errors.New("signing keys document has an empty or repeated kid")
		}

		seen[k.Keys[i].KID] = true

		if _, err := k.Keys[i].Private(); err != nil {
			return Keys{}, fmt.Errorf("signing key %q: %w", k.Keys[i].KID, err)
		}
	}

	return k, nil
}

// Marshal encodes the document.
func (k Keys) Marshal() ([]byte, error) {
	k.Version = KeysVersion

	return json.Marshal(k)
}

// Private decodes the key's private half.
func (k Key) Private() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(k.PEM))
	if block == nil {
		return nil, errors.New("not PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a PKCS#8 private key")
	}

	rk, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}

	if rk.N.BitLen() < MinKeyBits {
		return nil, fmt.Errorf("RSA key is %d bits, the minimum is %d", rk.N.BitLen(), MinKeyBits)
	}

	return rk, nil
}

// Signing returns the key tokens are signed with at now: the one with the latest ActiveFrom that
// has passed. ok is false when none has.
func (k Keys) Signing(now time.Time) (Key, bool) {
	active := slices.DeleteFunc(slices.Clone(k.Keys), func(c Key) bool { return c.ActiveFrom.After(now) })
	if len(active) == 0 {
		return Key{}, false
	}

	return slices.MaxFunc(active, func(a, b Key) int { return a.ActiveFrom.Compare(b.ActiveFrom) }), true
}

// Published returns the keys the JWKS lists at now: every key that has not been retired,
// including a successor that is not yet signing.
func (k Keys) Published(now time.Time) []Key {
	return slices.DeleteFunc(slices.Clone(k.Keys), func(c Key) bool {
		return c.RetireAfter != nil && !c.RetireAfter.After(now)
	})
}
