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
	"reflect"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

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
	ctx context.Context, c client.Reader, namespace string, cluster *ichorv1alpha1.TalosCluster,
) (*secrets.Bundle, error) {
	if namespace == "" {
		return nil, fmt.Errorf("loading secrets bundle: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	return secretsbundle.Load(ctx, c, namespace, cluster.Spec.SecretsRef.Name)
}

// bundleSecretMaterial lists every sensitive scalar in bundle. The one
// list leak_test.go's own assertions and the scrubber both
// use, so the two can never silently check (or redact) a different set
// of fields than each other.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	var out []string

	collectStrings(reflect.ValueOf(bundle), &out)

	return out
}

// collectStrings appends every non-empty string and []byte reachable from v (trimmed, so a PEM that lost
// its trailing newline in an error text is still matched). Deriving the list from the type means a field
// Talos adds to the bundle is scrubbed without anyone remembering to list it.
func collectStrings(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			collectStrings(v.Elem(), out)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				collectStrings(v.Field(i), out)
			}
		}
	case reflect.String:
		if s := strings.TrimSpace(v.String()); s != "" {
			*out = append(*out, s)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if s := strings.TrimSpace(string(v.Bytes())); s != "" {
				*out = append(*out, s)
			}
		}
	}
}

// redacted is what scrubber puts in place of a secret value.
const redacted = "[redacted]"

// scrubber removes secret values from text that is about to be stored in
// status or logged (ADR 0041, ADR 0047). Talos and S3 errors are not under
// our control: a future version could echo a header, a key or a bundle
// scalar, so scrubbing is unconditional and stays exercised. What counts as
// secret for the bundle is bundleSecretMaterial.
type scrubber struct{ secrets []string }

func (s *scrubber) add(v ...string) {
	for _, x := range v {
		if x != "" {
			s.secrets = append(s.secrets, x)
		}
	}
}

// clean replaces longest values first: were a secret a substring of another,
// replacing the short one first would leave the rest of the long one behind.
func (s *scrubber) clean(msg string) string {
	ordered := slices.Clone(s.secrets)
	slices.SortStableFunc(ordered, func(a, b string) int { return len(b) - len(a) })

	for _, x := range ordered {
		msg = strings.ReplaceAll(msg, x, redacted)
	}

	return msg
}
