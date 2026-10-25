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
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// bundleSecretKey is secretsbundle.SecretKey under this package's own
// name, kept so fixtures_test.go's existing fixture, keyed on it, doesn't
// need to change -- it is an alias, not a second definition: there is
// still exactly one literal value, in internal/secretsbundle (ADR 0033).
const bundleSecretKey = secretsbundle.SecretKey

// loadBundle reads cluster's secrets bundle via internal/secretsbundle.Load
// (ADR 0033) -- the same function internal/controller.loadBundle calls --
// so GetEffectiveConfig and NodeTalosClient dialing (a read-only identity
// minted from the same CA) render and connect against the same bundle the
// controller applied, and so a failure to read or decode it is guaranteed
// never to put the Secret's data into an error message (principle 2),
// enforced in the one place both callers share rather than two
// independently-written copies that could drift on that guarantee without
// either side's own leak tests noticing.
//
// namespace is the backend's own namespace (ControllerNamespace --
// POD_NAMESPACE in practice, see cmd/ichor-ui/main.go), never
// cluster.Namespace: a TalosCluster may live in any namespace this
// backend watches, but Kubernetes RBAC cannot express "the Secret this
// TalosCluster names, wherever that TalosCluster happens to live" at
// anything narrower than cluster-wide Secret read, so this package
// resolves in its own fixed namespace instead, exactly the reasoning ADR
// 0011 already settled for internal/controller's own loadBundle -- this
// function used to read cluster.Namespace directly instead, which is
// wrong for the same reason and only went unnoticed because every test
// fixture happened to put the TalosCluster and the Secret in the same
// namespace.
//
// namespace must be non-empty: Secret is a namespaced resource, and an
// empty namespace is never treated as "all namespaces" here -- that
// would turn a missing POD_NAMESPACE into a silent, far broader read than
// intended.
func loadBundle(
	ctx context.Context, c client.Client, namespace string, cluster *ichorv1alpha1.TalosCluster,
) (*secrets.Bundle, error) {
	if namespace == "" {
		return nil, fmt.Errorf("loading secrets bundle: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	return secretsbundle.Load(ctx, c, namespace, cluster.Spec.SecretsRef.Name)
}
