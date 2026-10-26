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
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RotationKey is the key, in the same Secret as SecretKey, under which a CA rotation keeps the new
// CAs (certificate and key) from the moment they are generated until the rotation is finalized
// (ADR 0063). It exists so that a crash can never leave nodes trusting a CA whose key nobody has.
const RotationKey = "rotation.yaml"

// Rotation is the new Talos API CA and Kubernetes API CA of a CA rotation in progress.
type Rotation struct {
	Talos      *x509.PEMEncodedCertificateAndKey `yaml:"talos"`
	Kubernetes *x509.PEMEncodedCertificateAndKey `yaml:"kubernetes"`
}

// NewRotation generates the two new CAs the way a new bundle does.
func NewRotation(now time.Time, contract *config.VersionContract) (*Rotation, error) {
	talosCA, err := secrets.NewTalosCA(now)
	if err != nil {
		return nil, fmt.Errorf("generating a Talos API CA: %w", err)
	}

	k8sCA, err := secrets.NewKubernetesCA(now, contract)
	if err != nil {
		return nil, fmt.Errorf("generating a Kubernetes API CA: %w", err)
	}

	return &Rotation{
		Talos:      &x509.PEMEncodedCertificateAndKey{Crt: talosCA.CrtPEM, Key: talosCA.KeyPEM},
		Kubernetes: &x509.PEMEncodedCertificateAndKey{Crt: k8sCA.CrtPEM, Key: k8sCA.KeyPEM},
	}, nil
}

// Validate reports whether r holds both CAs with a key. The message never includes their content.
func (r *Rotation) Validate() error {
	for name, ca := range map[string]*x509.PEMEncodedCertificateAndKey{"talos": r.Talos, "kubernetes": r.Kubernetes} {
		if ca == nil || len(ca.Crt) == 0 || len(ca.Key) == 0 {
			return fmt.Errorf("rotation CA %q is missing its certificate or key", name)
		}

		if _, err := Fingerprint(ca.Crt); err != nil {
			return fmt.Errorf("rotation CA %q: %w", name, err)
		}
	}

	return nil
}

// Marshal encodes r as the value of RotationKey.
func (r *Rotation) Marshal() ([]byte, error) {
	return yaml.Marshal(r)
}

// ParseRotation decodes and validates a RotationKey value.
func ParseRotation(raw []byte) (*Rotation, error) {
	var r Rotation
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return nil, errors.New("decoding the rotation CAs")
	}

	if err := r.Validate(); err != nil {
		return nil, err
	}

	return &r, nil
}

// Apply returns a copy of bundle with its Talos API CA and Kubernetes API CA replaced by r's. Every
// other field, including everything the etcd backup key derives from, is shared with bundle
// unchanged.
func (r *Rotation) Apply(bundle *secrets.Bundle) *secrets.Bundle {
	out := *bundle
	certs := *bundle.Certs
	certs.OS = r.Talos
	certs.K8s = r.Kubernetes
	out.Certs = &certs

	return &out
}

// LoadRotation reads RotationKey from the Secret named name in namespace. It returns nil, nil when
// the key is absent: no rotation is in progress.
func LoadRotation(ctx context.Context, c client.Reader, namespace, name string) (*Rotation, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[RotationKey]
	if !ok {
		return nil, nil //nolint:nilnil // absence is the answer
	}

	r, err := ParseRotation(raw)
	if err != nil {
		return nil, fmt.Errorf("reading %q from Secret %q: %w", RotationKey, key, err)
	}

	return r, nil
}

// Fingerprint is the SHA-256 of the first certificate in crtPEM, hex encoded. It is public.
func Fingerprint(crtPEM []byte) (string, error) {
	block, _ := pem.Decode(crtPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("not a PEM certificate")
	}

	sum := sha256.Sum256(block.Bytes)

	return hex.EncodeToString(sum[:]), nil
}

// BundleFingerprint identifies the two CAs a user's saved copy of the bundle must carry to still
// work: a fingerprint over the Talos API CA and the Kubernetes API CA.
func BundleFingerprint(bundle *secrets.Bundle) (string, error) {
	if bundle == nil || bundle.Certs == nil || bundle.Certs.OS == nil || bundle.Certs.K8s == nil {
		return "", errors.New("secrets bundle has no Talos or Kubernetes CA")
	}

	talos, err := Fingerprint(bundle.Certs.OS.Crt)
	if err != nil {
		return "", fmt.Errorf("talos CA: %w", err)
	}

	k8s, err := Fingerprint(bundle.Certs.K8s.Crt)
	if err != nil {
		return "", fmt.Errorf("kubernetes CA: %w", err)
	}

	sum := sha256.Sum256([]byte(talos + "/" + k8s))

	return hex.EncodeToString(sum[:]), nil
}
