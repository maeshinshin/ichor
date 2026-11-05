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

// Package secretsbundle reads a Talos secrets bundle out of the
// Kubernetes Secret a TalosCluster.spec.secretsRef names -- shared by
// internal/controller and internal/uiapi, which read the same
// bundle for different reasons (applying config; dialing a read-only
// Talos client) but under the same requirement: a failure to read or
// decode it must never put the Secret's own data into an error message
// (principle 2, secrets are referenced, never embedded). That property
// is exactly the kind two independently-written copies drift apart on
// without anyone noticing -- a change that loosens it in one copy only
// fails loudly in the package whose own tests happen to cover that path.
// Centralizing it here means there is only one place to get right, and
// only one place either caller's leak tests need to exercise.
package secretsbundle

import (
	"context"
	"fmt"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretKey is the key under which the secrets bundle lives in the Secret
// TalosCluster.spec.secretsRef names, encoded exactly the way `talosctl
// gen secrets` writes it and `ichor init` keeps it locally at
// `~/.ichor/clusters/<name>/secrets.yaml` (architecture.md's "Trust"
// section): YAML, under the key "secrets.yaml". That makes the local
// backup and the Secret's content the same bytes -- `kubectl create secret
// generic --from-file=secrets.yaml` works, and a recovery reading the
// Secret back out gets a file talosctl already understands.
const SecretKey = "secrets.yaml"

// Load reads the Secret named name in namespace via c and decodes
// SecretKey as a secrets.Bundle. Every error path below is structural
// (missing Secret, missing key, malformed YAML) and never includes the
// Secret's data in its message.
//
// namespace and name are the caller's own to resolve: internal/controller
// always reads in the controller's own fixed namespace regardless of
// which namespace the TalosCluster lives in;
// internal/uiapi reads in the TalosCluster's own namespace. Centralizing
// the read-and-decode logic here does not require centralizing that
// choice too, so this function takes the resolved namespace and name
// directly rather than a *TalosCluster it would have to resolve itself.
func Load(ctx context.Context, c client.Reader, namespace, name string) (*secrets.Bundle, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[SecretKey]
	if !ok {
		return nil, fmt.Errorf("secrets bundle Secret %q has no %q key", key, SecretKey)
	}

	bundle, err := Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("decoding secrets bundle from Secret %q: %w", key, err)
	}

	return bundle, nil
}

// loadKey reads data key dataKey from the Secret named name in namespace and parses it. It returns nil, nil
// when the key is absent.
func loadKey[T any](ctx context.Context, c client.Reader, namespace, name, dataKey string, parse func([]byte) (*T, error)) (*T, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: namespace, Name: name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[dataKey]
	if !ok {
		return nil, nil //nolint:nilnil // absence is the answer
	}

	v, err := parse(raw)
	if err != nil {
		return nil, fmt.Errorf("reading %q from Secret %q: %w", dataKey, key, err)
	}

	return v, nil
}

// Decode parses a secrets bundle in the "secrets.yaml" encoding and fills in what the encoding cannot carry.
// Every loader (the Secret, a local file) goes through it so none can forget the Clock.
func Decode(raw []byte) (*secrets.Bundle, error) {
	var bundle secrets.Bundle
	if err := yaml.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}

	// Bundle.Clock carries "yaml:\"-\"" (it is behavior, not data) and the machinery
	// package's own populate() is the only other place that fills it in -- which only
	// runs while building a brand-new bundle, never while decoding an existing one back
	// out of a Secret. Left nil, the first GenerateTalosAPIClientCertificate call on it
	// panics on a nil-interface Clock.Now() -- confirmed for real:
	// internal/controller.NewMachineryTalosClient dialing with a bundle loaded exactly
	// this way. No test anywhere had exercised that combination before: every existing
	// test faked the Talos client entirely, and every real-hardware pass either had no bundle or never got a working one.
	if bundle.Clock == nil {
		bundle.Clock = backdatedClock{}
	}

	return &bundle, nil
}

// clockSkew is how far a loaded bundle's clock runs behind: the certificates it mints (the os:admin
// client certificate, minted per dial) then carry a NotBefore that a node whose clock is a little
// behind the controller's still accepts. Without it such a node answers the TLS handshake with
// "expired certificate" (Go reports a not-yet-valid certificate that way), which pauses an operation
// until the next dial. The lifetime is a year, so the shift costs nothing.
const clockSkew = 5 * time.Minute

type backdatedClock struct{}

func (backdatedClock) Now() time.Time { return time.Now().Add(-clockSkew) }
