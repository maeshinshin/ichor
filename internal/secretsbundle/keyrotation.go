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

package secretsbundle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KeyRotationKey is the key, in the same Secret as SecretKey, under which a key rotation keeps the new
// service-account signing key and secretbox secret from the moment they are generated until the rotation
// is finalized (ADR 0121). Like RotationKey it exists so that a crash can never leave nodes using a key
// nobody holds.
const KeyRotationKey = "keyrotation.yaml"

// RetiredKeysKey is the key, in the same Secret as SecretKey and in a file of the same name next to the
// exported bundle, under which the keys a rotation retired are kept: the secretbox secrets the etcd backup
// key of older backups derives from, and the fingerprints of retired service-account keys (ADR 0121).
const RetiredKeysKey = "retired-keys.yaml"

// The keys a key rotation can replace.
const (
	TargetServiceAccount = "serviceAccount"
	TargetSecretbox      = "secretbox"
)

// secretboxKeyBytes is the length of a secretbox key: kube-apiserver rejects any other.
const secretboxKeyBytes = 32

// KeyRotation is the new service-account signing key and secretbox secret of a key rotation in
// progress; a field is absent for a key the rotation does not replace.
type KeyRotation struct {
	ServiceAccount *x509.PEMEncodedKey `yaml:"serviceAccount,omitempty"`
	Secretbox      string              `yaml:"secretbox,omitempty"`
}

// NewKeyRotation generates the new keys for targets. The service-account key has the algorithm of the
// current one, so what verifies tokens outside the cluster keeps working with either.
func NewKeyRotation(bundle *secrets.Bundle, targets []string) (*KeyRotation, error) {
	if len(targets) == 0 {
		return nil, errors.New("a key rotation needs at least one target")
	}

	var kr KeyRotation

	for _, target := range targets {
		switch target {
		case TargetServiceAccount:
			key, err := newServiceAccountKey(bundle)
			if err != nil {
				return nil, err
			}

			kr.ServiceAccount = key
		case TargetSecretbox:
			buf := make([]byte, secretboxKeyBytes)
			if _, err := rand.Read(buf); err != nil {
				return nil, fmt.Errorf("generating a secretbox key: %w", err)
			}

			kr.Secretbox = base64.StdEncoding.EncodeToString(buf)
		default:
			return nil, fmt.Errorf("%q is not a key this rotation replaces (%s, %s)", target, TargetServiceAccount, TargetSecretbox)
		}
	}

	return &kr, nil
}

func newServiceAccountKey(bundle *secrets.Bundle) (*x509.PEMEncodedKey, error) {
	if bundle == nil || bundle.Certs == nil || bundle.Certs.K8sServiceAccount == nil {
		return nil, errors.New("secrets bundle has no service-account key")
	}

	if _, err := bundle.Certs.K8sServiceAccount.GetRSAKey(); err == nil {
		key, err := x509.NewRSAKey()
		if err != nil {
			return nil, fmt.Errorf("generating a service-account key: %w", err)
		}

		return &x509.PEMEncodedKey{Key: key.KeyPEM}, nil
	}

	key, err := x509.NewECDSAKey()
	if err != nil {
		return nil, fmt.Errorf("generating a service-account key: %w", err)
	}

	return &x509.PEMEncodedKey{Key: key.KeyPEM}, nil
}

// Validate reports whether kr holds at least one well-formed key. The message never includes key material.
func (kr *KeyRotation) Validate() error {
	if kr.ServiceAccount == nil && kr.Secretbox == "" {
		return errors.New("key rotation holds no key")
	}

	if kr.ServiceAccount != nil {
		if _, err := kr.ServiceAccount.GetKey(); err != nil {
			return errors.New("the new service-account key is not a PEM private key")
		}
	}

	if kr.Secretbox != "" {
		raw, err := base64.StdEncoding.DecodeString(kr.Secretbox)
		if err != nil || len(raw) != secretboxKeyBytes {
			return fmt.Errorf("the new secretbox secret is not %d bytes of standard base64", secretboxKeyBytes)
		}
	}

	return nil
}

// Marshal encodes kr as the value of KeyRotationKey.
func (kr *KeyRotation) Marshal() ([]byte, error) {
	return yaml.Marshal(kr)
}

// ParseKeyRotation decodes and validates a KeyRotationKey value.
func ParseKeyRotation(raw []byte) (*KeyRotation, error) {
	var kr KeyRotation
	if err := yaml.Unmarshal(raw, &kr); err != nil {
		return nil, errors.New("decoding the new keys")
	}

	if err := kr.Validate(); err != nil {
		return nil, err
	}

	return &kr, nil
}

// Apply returns a copy of bundle with the service-account key and the secretbox secret replaced by kr's,
// for the keys kr holds. Every other field is shared with bundle unchanged.
func (kr *KeyRotation) Apply(bundle *secrets.Bundle) *secrets.Bundle {
	out := *bundle

	if kr.ServiceAccount != nil {
		certs := *bundle.Certs
		certs.K8sServiceAccount = kr.ServiceAccount
		out.Certs = &certs
	}

	if kr.Secretbox != "" {
		s := *bundle.Secrets
		s.SecretboxEncryptionSecret = kr.Secretbox
		out.Secrets = &s
	}

	return &out
}

// LoadKeyRotation reads KeyRotationKey from the Secret named name in namespace. It returns nil, nil when
// the key is absent: no key rotation is in progress.
func LoadKeyRotation(ctx context.Context, c client.Reader, namespace, name string) (*KeyRotation, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[KeyRotationKey]
	if !ok {
		return nil, nil //nolint:nilnil // absence is the answer
	}

	kr, err := ParseKeyRotation(raw)
	if err != nil {
		return nil, fmt.Errorf("reading %q from Secret %q: %w", KeyRotationKey, key, err)
	}

	return kr, nil
}

// ServiceAccountFingerprint is the SHA-256 of the DER of key's public half, hex encoded. It is public, and
// identifies a service-account key in status without revealing it.
func ServiceAccountFingerprint(key *x509.PEMEncodedKey) (string, error) {
	if key == nil {
		return "", errors.New("no service-account key")
	}

	k, err := key.GetKey()
	if err != nil {
		return "", errors.New("not a PEM key")
	}

	block, _ := pem.Decode(k.GetPublicKeyPEM())
	if block == nil {
		return "", errors.New("not a PEM key")
	}

	sum := sha256.Sum256(block.Bytes)

	return hex.EncodeToString(sum[:]), nil
}

// SecretboxID names a secretbox secret without disclosing it: a domain-separated hash cut to 64 bits,
// enough to tell the keys of a rotation apart in status.
func SecretboxID(secret string) string {
	sum := sha256.Sum256([]byte("ichor.maesh.dev/secretbox-id\x00" + secret))

	return hex.EncodeToString(sum[:8])
}

// RetiredKey is what one key rotation retired.
type RetiredKey struct {
	RetiredAt time.Time `yaml:"retiredAt"`
	// Secretbox is the retired secretbox secret, kept so that backups sealed with the key derived from it
	// stay readable. Empty when the rotation did not replace the secretbox key.
	Secretbox string `yaml:"secretbox,omitempty"`
	// ServiceAccount is the fingerprint of the retired service-account public key, kept so that a copy of
	// the bundle from before the rotation is recognised as stale. Empty when that key was not replaced.
	ServiceAccount string `yaml:"serviceAccount,omitempty"`
}

// Retired is the list of everything key rotations retired, oldest first.
type Retired struct {
	Keys []RetiredKey `yaml:"retired"`
}

// Add appends k as the newest retired key.
func (r *Retired) Add(k RetiredKey) { r.Keys = append(r.Keys, k) }

// Secretboxes returns the retired secretbox secrets, newest first. A nil Retired retires nothing.
func (r *Retired) Secretboxes() []string {
	if r == nil {
		return nil
	}

	var out []string

	for _, k := range slices.Backward(r.Keys) {
		if k.Secretbox != "" {
			out = append(out, k.Secretbox)
		}
	}

	return out
}

// Fingerprint identifies the list for a copy of the bundle to be compared against; it is "" for an empty one.
func (r *Retired) Fingerprint() string {
	if r == nil || len(r.Keys) == 0 {
		return ""
	}

	h := sha256.New()

	for _, k := range r.Keys {
		_, _ = fmt.Fprintf(h, "%d|%s|%s\n", k.RetiredAt.UTC().Unix(), SecretboxID(k.Secretbox), k.ServiceAccount)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// Marshal encodes r as the value of RetiredKeysKey.
func (r *Retired) Marshal() ([]byte, error) { return yaml.Marshal(r) }

// ParseRetired decodes a RetiredKeysKey value. The message never includes its content.
func ParseRetired(raw []byte) (*Retired, error) {
	var r Retired
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, errors.New("decoding the retired keys")
	}

	return &r, nil
}

// LoadRetired reads RetiredKeysKey from the Secret named name in namespace. An absent key is an empty list.
func LoadRetired(ctx context.Context, c client.Reader, namespace, name string) (*Retired, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[RetiredKeysKey]
	if !ok {
		return &Retired{}, nil
	}

	r, err := ParseRetired(raw)
	if err != nil {
		return nil, fmt.Errorf("reading %q from Secret %q: %w", RetiredKeysKey, key, err)
	}

	return r, nil
}
