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
	"fmt"

	"github.com/siderolabs/crypto/x509"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/rotatepatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// CARotation is the system-layer input of a CA rotation in progress: the stage the
// nodes are being taken to and the new CAs. The bundle Render is given stays the one the nodes were
// built from (the old CAs); the stage decides which CA issues and which are accepted.
//
//	Accepting: old CA issues, new CA is accepted.
//	Issuing:   new CA issues, old CA is accepted.
//	Dropping, Finalizing: new CA issues, nothing else is accepted -- byte for byte the
//	           configuration a bundle that already holds the new CAs renders, which is what
//	           keeps the machine controller from seeing drift when the rotation ends.
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
		return bundle, &acceptedCAs{talos: r.New.Talos.Crt, kubernetes: r.New.Kubernetes.Crt}, nil
	case ichorv1alpha1.CARotationStageIssuing:
		return r.New.Apply(bundle), &acceptedCAs{talos: bundle.Certs.OS.Crt, kubernetes: bundle.Certs.K8s.Crt}, nil
	case ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing:
		return r.New.Apply(bundle), nil, nil
	default:
		return nil, nil, fmt.Errorf("render: CA rotation stage %q is not one a configuration is rendered for", r.Stage)
	}
}

type acceptedCAs struct{ talos, kubernetes []byte }

func (s *SystemInputs) caRotation() *CARotation {
	if s == nil {
		return nil
	}

	return s.CARotation
}

// injectAcceptedCAs adds the extra accepted CAs: machine.acceptedCAs for the Talos API and, in
// whichever form the version contract uses, the Kubernetes API's. These are the same fields
// `talosctl rotate-ca` patches (pkg/rotate/pki), written through machinery's own rotatepatcher.
func injectAcceptedCAs(cfg coreconfig.Provider, accepted *acceptedCAs) (coreconfig.Provider, error) {
	if accepted == nil {
		return cfg, nil
	}

	cfg, err := cfg.PatchV1Alpha1(func(c *v1alpha1.Config) error {
		c.MachineConfig.MachineAcceptedCAs = append(c.MachineConfig.MachineAcceptedCAs,
			&x509.PEMEncodedCertificate{Crt: accepted.talos})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("render: adding the accepted Talos CA: %w", err)
	}

	cfg, err = rotatepatcher.K8sAddAcceptedCA(accepted.kubernetes)(cfg)
	if err != nil {
		return nil, fmt.Errorf("render: adding the accepted Kubernetes CA: %w", err)
	}

	return cfg, nil
}
