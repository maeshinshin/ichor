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

package uiapi

import (
	"context"
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// bundleSecretKey must stay byte-for-byte the same key
// internal/controller/bundle.go reads: both decode the one Secret
// TalosCluster.spec.secretsRef names, under the same "secrets.yaml" key
// architecture.md's "Trust" section fixes.
//
// shortcut: duplicated rather than imported, since internal/controller's
// loadBundle is unexported and internal/controller is the controller's own
// package, not a shared one; if the two ever drift, the symptom is loud
// (every render this package attempts fails to decode) rather than silent.
const bundleSecretKey = "secrets.yaml"

// loadBundle reads cluster's secrets bundle the same way
// internal/controller.loadBundle does, so GetEffectiveConfig and
// NodeTalosClient dialing (a read-only identity minted from the same CA)
// render and connect against the same bundle the controller applied.
func loadBundle(ctx context.Context, c client.Client, cluster *ichorv1alpha1.TalosCluster) (*secrets.Bundle, error) {
	var secret corev1.Secret

	key := types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Spec.SecretsRef.Name}
	if err := c.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("getting secrets bundle Secret %q: %w", key, err)
	}

	raw, ok := secret.Data[bundleSecretKey]
	if !ok {
		return nil, fmt.Errorf("secrets bundle Secret %q has no %q key", key, bundleSecretKey)
	}

	var bundle secrets.Bundle
	if err := yaml.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decoding secrets bundle from Secret %q: %w", key, err)
	}

	return &bundle, nil
}
