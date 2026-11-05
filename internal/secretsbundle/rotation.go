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
	"strings"
	"time"

	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// RotationKey is the key, in the same Secret as SecretKey, under which a CA rotation keeps the new
// CAs (certificate and key) from the moment they are generated until the rotation is finalized
// (ADR 0063). It exists so that a crash can never leave nodes trusting a CA whose key nobody has.
const RotationKey = "rotation.yaml"

// Rotation is the new CAs of a CA rotation in progress. A CA that is not rotated is nil; the set of
// CAs present is the rotation's targets, fixed when it started (ADR 0120). Rotations saved by an
// earlier Ichor hold the Talos and Kubernetes CAs only.
type Rotation struct {
	Talos      *x509.PEMEncodedCertificateAndKey `yaml:"talos,omitempty"`
	Kubernetes *x509.PEMEncodedCertificateAndKey `yaml:"kubernetes,omitempty"`
	Etcd       *x509.PEMEncodedCertificateAndKey `yaml:"etcd,omitempty"`
	Aggregator *x509.PEMEncodedCertificateAndKey `yaml:"aggregator,omitempty"`
}

// NewRotation generates the new Talos API CA and Kubernetes API CA the way a new bundle does.
func NewRotation(now time.Time, contract *config.VersionContract) (*Rotation, error) {
	return NewRotationFor(now, contract, ichorv1alpha1.DefaultCARotationTargets())
}

// NewRotationFor generates the new CAs for targets (the ichorv1alpha1.CARotationTarget* names).
func NewRotationFor(now time.Time, contract *config.VersionContract, targets []string) (*Rotation, error) {
	if len(targets) == 0 {
		return nil, errors.New("a rotation needs at least one CA to replace")
	}

	var (
		r   Rotation
		err error
	)

	for _, target := range targets {
		var (
			ca   *x509.CertificateAuthority
			into **x509.PEMEncodedCertificateAndKey
		)

		switch target {
		case ichorv1alpha1.CARotationTargetTalos:
			ca, err = secrets.NewTalosCA(now)
			into = &r.Talos
		case ichorv1alpha1.CARotationTargetKubernetes:
			ca, err = secrets.NewKubernetesCA(now, contract)
			into = &r.Kubernetes
		case ichorv1alpha1.CARotationTargetEtcd:
			ca, err = secrets.NewEtcdCA(now, contract)
			into = &r.Etcd
		case ichorv1alpha1.CARotationTargetAggregator:
			ca, err = secrets.NewAggregatorCA(now, contract)
			into = &r.Aggregator
		default:
			return nil, fmt.Errorf("%q is not a CA this rotation can replace", target)
		}

		if err != nil {
			return nil, fmt.Errorf("generating the new %s CA: %w", target, err)
		}

		*into = &x509.PEMEncodedCertificateAndKey{Crt: ca.CrtPEM, Key: ca.KeyPEM}
	}

	return &r, nil
}

// named lists the rotation's CAs with their target names, in the fixed order of Targets.
func (r *Rotation) named() []struct {
	Name string
	CA   *x509.PEMEncodedCertificateAndKey
} {
	return []struct {
		Name string
		CA   *x509.PEMEncodedCertificateAndKey
	}{
		{ichorv1alpha1.CARotationTargetTalos, r.Talos},
		{ichorv1alpha1.CARotationTargetKubernetes, r.Kubernetes},
		{ichorv1alpha1.CARotationTargetEtcd, r.Etcd},
		{ichorv1alpha1.CARotationTargetAggregator, r.Aggregator},
	}
}

// Targets lists the CAs r replaces, in a fixed order: talos, kubernetes, etcd, kubernetes-aggregator.
func (r *Rotation) Targets() []string {
	var out []string

	for _, n := range r.named() {
		if n.CA != nil {
			out = append(out, n.Name)
		}
	}

	return out
}

// Validate reports whether r holds at least one CA and every CA it holds has a certificate and a
// key. The message never includes their content.
func (r *Rotation) Validate() error {
	if len(r.Targets()) == 0 {
		return errors.New("the rotation holds no CA")
	}

	for _, n := range r.named() {
		if n.CA == nil {
			continue
		}

		if len(n.CA.Crt) == 0 || len(n.CA.Key) == 0 {
			return fmt.Errorf("rotation CA %q is missing its certificate or key", n.Name)
		}

		if _, err := Fingerprint(n.CA.Crt); err != nil {
			return fmt.Errorf("rotation CA %q: %w", n.Name, err)
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

// Apply returns a copy of bundle with the CAs r holds replaced. Every other field, including
// everything the etcd backup key derives from, is shared with bundle unchanged.
func (r *Rotation) Apply(bundle *secrets.Bundle) *secrets.Bundle {
	out := *bundle
	certs := *bundle.Certs

	if r.Talos != nil {
		certs.OS = r.Talos
	}

	if r.Kubernetes != nil {
		certs.K8s = r.Kubernetes
	}

	if r.Etcd != nil {
		certs.Etcd = r.Etcd
	}

	if r.Aggregator != nil {
		certs.K8sAggregator = r.Aggregator
	}

	out.Certs = &certs

	return &out
}

// LoadRotation reads RotationKey from the Secret named name in namespace. It returns nil, nil when
// the key is absent: no rotation is in progress.
func LoadRotation(ctx context.Context, c client.Reader, namespace, name string) (*Rotation, error) {
	return loadKey(ctx, c, namespace, name, RotationKey, ParseRotation)
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

// ExportFingerprint identifies what a saved copy of the bundle must carry to recover the cluster
// from: BundleFingerprint's two CAs, the etcd and aggregator CAs (ADR 0120) and, once a key rotation
// has happened, the keys it retired (ADR 0121; retired may be nil). `bundle-export` and the
// cert-expiry reconciler compare it; the credentials code, which only needs the Talos and
// Kubernetes CAs, keeps BundleFingerprint. A bundle without an etcd or aggregator CA counts them empty.
// A cluster that never rotated its keys keeps the value it had before ADR 0121, so existing
// acknowledgements stay valid; a retired key changes it, so an export from before the rotation does not
// clear BundleBackupStale.
func ExportFingerprint(bundle *secrets.Bundle, retired *Retired) (string, error) {
	base, err := BundleFingerprint(bundle)
	if err != nil {
		return "", err
	}

	parts := []string{base}

	for _, ca := range []*x509.PEMEncodedCertificateAndKey{bundle.Certs.Etcd, bundle.Certs.K8sAggregator} {
		fp := ""

		if ca != nil {
			if fp, err = Fingerprint(ca.Crt); err != nil {
				return "", err
			}
		}

		parts = append(parts, fp)
	}

	if rf := retired.Fingerprint(); rf != "" {
		parts = append(parts, rf)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))

	return hex.EncodeToString(sum[:]), nil
}
