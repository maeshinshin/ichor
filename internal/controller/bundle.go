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
	"context"
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// bundleSecretKey is the key under which the secrets bundle lives in the
// Secret TalosCluster.spec.secretsRef names, encoded exactly the way
// `talosctl gen secrets` writes it and `ichor init` keeps it locally at
// `~/.ichor/clusters/<name>/secrets.yaml` (architecture.md's "Trust"
// section): YAML, under the key "secrets.yaml". That makes the local
// backup and the Secret's content the same bytes -- `kubectl create secret
// generic --from-file=secrets.yaml` works, and a recovery reading the
// Secret back out gets a file talosctl already understands.
const bundleSecretKey = "secrets.yaml"

// loadBundle implements design step 1's "load the bundle from the Secret":
// it reads the Secret cluster.Spec.SecretsRef names, in cluster's
// namespace, and decodes bundleSecretKey as a secrets.Bundle. It returns
// the bundle to the caller and stores nothing else about it (principle 2:
// secrets are referenced, never embedded) -- in particular, every error
// path below is structural (missing Secret, missing key, malformed YAML)
// and never includes the Secret's data in its message.
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
