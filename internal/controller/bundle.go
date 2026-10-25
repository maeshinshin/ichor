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
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// bundleSecretKey is secretsbundle.SecretKey under this package's own
// name, kept so existing fixtures (leak_test.go,
// talosmachine_controller_test.go) that build a Secret keyed on it don't
// need to change -- it is an alias, not a second definition: there is
// still exactly one literal value, in internal/secretsbundle (ADR 0033).
const bundleSecretKey = secretsbundle.SecretKey

// loadBundle implements design step 1's "load the bundle from the Secret":
// it reads the Secret cluster.Spec.SecretsRef names, in the controller's own
// namespace (namespace -- POD_NAMESPACE in practice, see cmd/main.go), via
// internal/secretsbundle.Load (ADR 0033), which is what actually enforces
// that a failure to read or decode it never puts the Secret's data into an
// error message (principle 2: secrets are referenced, never embedded) --
// shared with internal/uiapi's own copy of this same call, rather than two
// independently-written decoders that could drift on that guarantee without
// either side's leak tests noticing.
//
// A TalosCluster may live in any namespace the controller watches, but
// Kubernetes RBAC cannot express "the Secret this TalosCluster names,
// wherever that TalosCluster happens to live" at anything narrower than
// cluster-wide Secret read; resolving in the controller's fixed namespace
// instead is what lets the RBAC marker on Reconcile stay namespace-scoped
// (ADR 0011).
//
// namespace must be non-empty: Secret is a namespaced resource, and an empty
// namespace is never treated as "all namespaces" here -- that would turn a
// missing POD_NAMESPACE into a silent, far broader read than intended.
func loadBundle(
	ctx context.Context, c client.Client, namespace string, cluster *ichorv1alpha1.TalosCluster,
) (*secrets.Bundle, error) {
	if namespace == "" {
		return nil, fmt.Errorf("loading secrets bundle: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	return secretsbundle.Load(ctx, c, namespace, cluster.Spec.SecretsRef.Name)
}

// bundleSecretMaterial lists every sensitive scalar in bundle. The one
// list leak_test.go's own assertions and redactBundleMaterial below both
// use, so the two can never silently check (or redact) a different set
// of fields than each other.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	return []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.ID,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Crt),
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Crt),
		string(bundle.Certs.K8s.Key),
	}
}

// redactBundleMaterial replaces any of bundle's own secret-shaped scalars
// found verbatim in message with a fixed placeholder.
//
// This exists for exactly one caller: setWarnings, which joins Talos's own
// dry-run warning strings into a TalosMachine status Condition -- a field
// read far more broadly than Secret RBAC allows. Verified against Talos
// v1.14.3's actual source (pkg/machinery/config/container/validate.go's
// validate/runtimeValidate, and every warning string machinery's own
// v1alpha1 config validators generate) that no warning today ever echoes
// a submitted value back, only field names, deprecation notices and
// Talos's own constants (ADR 0041 records exactly what was read). That
// finding is about today's warning text, not about this field: a future
// Talos release could start echoing a value without this package's own
// code changing at all, and a redaction step that only runs when today's
// known-safe assumption breaks is a redaction step that was never
// actually exercised. Unconditional, so it stays exercised.
func redactBundleMaterial(message string, bundle *secrets.Bundle) string {
	for _, material := range bundleSecretMaterial(bundle) {
		if material == "" {
			continue
		}

		message = strings.ReplaceAll(message, material, "[redacted]")
	}

	return message
}
