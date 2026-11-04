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

package render

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/siderolabs/crypto/x509"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/rotatepatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// CARotation is the system-layer input of a CA rotation in progress (ADR 0063, 0120): the stage the
// nodes are being taken to and the new CAs. The bundle Render is given stays the one the nodes were
// built from (the old CAs); the stage decides which CA issues and which are accepted. A CA the
// rotation does not hold (New's field is nil) renders as before.
//
//	Accepting: old CA issues, new CA is accepted.
//	Issuing:   new CA issues, old CA is accepted.
//	Dropping, Finalizing: new CA issues, nothing else is accepted -- byte for byte the
//	           configuration a bundle that already holds the new CAs renders, which is what
//	           keeps the machine controller from seeing drift when the rotation ends.
//
// The Talos, Kubernetes and aggregator CAs have an accepted list in the machine configuration. The
// etcd CA has none: both certificates are written into the one field (a PEM bundle, which Talos
// copies verbatim into the files etcd and kube-apiserver trust), with the signing CA first and its
// key (Talos signs with the first block, ADR 0120).
//
// Only the rotation executor sets it. ResolveSystemInputs never does, and the machine controller
// does not render, dial or apply for a cluster while a rotation is in progress.
type CARotation struct {
	Stage string
	New   *secretsbundle.Rotation
}

// apply returns the bundle the base configuration is generated from and the CAs to add to the
// accepted lists, per stage.
func (r *CARotation) apply(bundle *secrets.Bundle) (*secrets.Bundle, *acceptedCAs, error) {
	if r == nil {
		return bundle, nil, nil
	}

	if r.New == nil {
		return nil, nil, fmt.Errorf("render: CA rotation has no new CAs")
	}

	switch r.Stage {
	case ichorv1alpha1.CARotationStageAccepting:
		base := bundle

		if r.New.Etcd != nil {
			base = withEtcd(bundle, trustBundle(bundle.Certs.Etcd, r.New.Etcd.Crt))
		}

		return base, &acceptedCAs{
			talos: crtOf(r.New.Talos), kubernetes: crtOf(r.New.Kubernetes), aggregator: crtOf(r.New.Aggregator),
		}, nil
	case ichorv1alpha1.CARotationStageIssuing:
		base := r.New.Apply(bundle)

		if r.New.Etcd != nil {
			base = withEtcd(base, trustBundle(r.New.Etcd, bundle.Certs.Etcd.Crt))
		}

		return base, &acceptedCAs{
			talos: oldOf(r.New.Talos, bundle.Certs.OS), kubernetes: oldOf(r.New.Kubernetes, bundle.Certs.K8s),
			aggregator: oldOf(r.New.Aggregator, bundle.Certs.K8sAggregator),
		}, nil
	case ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing:
		return r.New.Apply(bundle), nil, nil
	default:
		return nil, nil, fmt.Errorf("render: CA rotation stage %q is not one a configuration is rendered for", r.Stage)
	}
}

// acceptedCAs are the certificates added to the accepted lists; nil means the CA is not rotated.
type acceptedCAs struct{ talos, kubernetes, aggregator []byte }

func crtOf(ca *x509.PEMEncodedCertificateAndKey) []byte {
	if ca == nil {
		return nil
	}

	return ca.Crt
}

// oldOf is the certificate of the CA being replaced, when next replaces it.
func oldOf(next, current *x509.PEMEncodedCertificateAndKey) []byte {
	if next == nil || current == nil {
		return nil
	}

	return current.Crt
}

// withEtcd returns a copy of bundle with the etcd CA field replaced.
func withEtcd(bundle *secrets.Bundle, etcd *x509.PEMEncodedCertificateAndKey) *secrets.Bundle {
	out := *bundle
	certs := *bundle.Certs
	certs.Etcd = etcd
	out.Certs = &certs

	return &out
}

// trustBundle is the etcd CA field that trusts signer's certificate and otherCrt and signs with signer:
// the signer's certificate first (Talos reads the first PEM block as the CA), then the other one, and
// the signer's key.
func trustBundle(signer *x509.PEMEncodedCertificateAndKey, otherCrt []byte) *x509.PEMEncodedCertificateAndKey {
	crt := slices.Clone(signer.Crt)
	if len(crt) > 0 && crt[len(crt)-1] != '\n' {
		crt = append(crt, '\n')
	}

	return &x509.PEMEncodedCertificateAndKey{Crt: append(crt, otherCrt...), Key: signer.Key}
}

// injectAcceptedCAs adds the extra accepted CAs: machine.acceptedCAs for the Talos API and, in
// whichever form the version contract uses, the Kubernetes API's. These are the same fields
// `talosctl rotate-ca` patches (pkg/rotate/pki), written through machinery's own rotatepatcher.
// The aggregator CA's accepted list is the typed KubeAggregatorCAConfig document of a control plane.
func injectAcceptedCAs(cfg coreconfig.Provider, accepted *acceptedCAs) (coreconfig.Provider, error) {
	if accepted == nil {
		return cfg, nil
	}

	var err error

	if accepted.talos != nil {
		cfg, err = cfg.PatchV1Alpha1(func(c *v1alpha1.Config) error {
			c.MachineConfig.MachineAcceptedCAs = append(c.MachineConfig.MachineAcceptedCAs,
				&x509.PEMEncodedCertificate{Crt: accepted.talos})

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("render: adding the accepted Talos CA: %w", err)
		}
	}

	if accepted.kubernetes != nil {
		cfg, err = rotatepatcher.K8sAddAcceptedCA(accepted.kubernetes)(cfg)
		if err != nil {
			return nil, fmt.Errorf("render: adding the accepted Kubernetes CA: %w", err)
		}
	}

	if accepted.aggregator != nil {
		cfg, err = addAggregatorAcceptedCA(cfg, accepted.aggregator)
		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// addAggregatorAcceptedCA appends crt to the accepted CAs of a control plane's KubeAggregatorCAConfig.
// Workers have no such document. A control plane without it is on a version contract older than
// v1.14, where the aggregator CA is one field with no accepted list: there is no stage with two.
func addAggregatorAcceptedCA(cfg coreconfig.Provider, crt []byte) (coreconfig.Provider, error) {
	if !cfg.Has(k8s.KubeAggregatorCAConfig) {
		if cfg.Machine().Type().IsControlPlane() {
			return nil, errors.New("render: the aggregator CA has an accepted list only from version contract v1.14; " +
				"it cannot be rotated on this cluster")
		}

		return cfg, nil
	}

	out, err := container.PatchDocument(cfg, func(doc *k8s.KubeAggregatorCAConfigV1Alpha1) error {
		if !slices.ContainsFunc(doc.AggregatorAcceptedCAs, func(ca string) bool { return bytes.Equal([]byte(ca), crt) }) {
			doc.AggregatorAcceptedCAs = append(doc.AggregatorAcceptedCAs, string(crt))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render: adding the accepted aggregator CA: %w", err)
	}

	return out, nil
}
