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
	"context"
	"fmt"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// registryAuthPasswordKey, registryAuthAuthKey and registryAuthIdentityTokenKey are the keys
// ResolveRegistryAuth looks for in the Secret a TalosClusterRegistry.authSecretRef names. They
// are the three RegistryAuthConfig fields's secretFields table drops from the mirror
// types -- "(user credential, not a bundle value)" -- precisely because a TalosConfigPatch must
// never be able to carry them; this is where they become expressible again, through a reference
// instead of a value on a CR. At least one of the three must be present for a registry that sets
// authSecretRef at all.
const (
	registryAuthPasswordKey      = "password"
	registryAuthAuthKey          = "auth"
	registryAuthIdentityTokenKey = "identityToken"
)

// RegistryAuth is one registry's resolved password-shaped pull credentials -- the three fields
// ResolveRegistryAuth reads out of a Secret, matching machinery's own RegistryAuthConfig minus
// its one non-secret field (username, which travels on TalosClusterRegistry.Username directly,
// never through this type). The only things ever done with a RegistryAuth are holding it in
// memory long enough for injectRegistries to merge it into the rendered machine config and
// letting it go out of scope: it is never logged, never put in an error string, and never
// written to any CR's spec or status (design principle 2).
type RegistryAuth struct {
	Password      string
	Auth          string
	IdentityToken string
}

// ResolveRegistryAuth reads the Secret named by each entry in
// cluster.Spec.Registries[].AuthSecretRef, in the controller's own namespace -- the same
// reasoning as internal/controller's loadBundle: RBAC cannot scope a Secret read to
// "wherever this TalosCluster happens to live" below cluster-wide, so every lookup here resolves
// against one fixed namespace regardless of which namespace the TalosCluster itself is in.
//
// A registry entry with no AuthSecretRef is anonymous and is simply absent from the returned
// map -- a public mirror needs no credentials, and that is not an error. A registry that does
// name a Secret fails loudly, never silently, if that Secret is missing or carries none of the
// three recognized keys: rendering it as an anonymous mirror instead would look like a working
// configuration until the first pull fails on the node, with nothing in Ichor's own output
// pointing at why.
//
// namespace must be non-empty, for the same reason loadBundle requires it: Secret is a
// namespaced resource, and an empty namespace is never treated as "all namespaces" here.
//
// Every error path below is structural (missing Secret, missing key) and never includes the
// Secret's data; see TestResolveRegistryAuthLeak.
func ResolveRegistryAuth(
	ctx context.Context, c client.Client, namespace string, cluster *ichorv1alpha1.TalosCluster,
) (map[string]RegistryAuth, error) {
	if namespace == "" {
		return nil, fmt.Errorf("resolving registry auth: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	resolved := make(map[string]RegistryAuth, len(cluster.Spec.Registries))

	for _, reg := range cluster.Spec.Registries {
		if reg.AuthSecretRef == nil {
			continue
		}

		var secret corev1.Secret

		key := types.NamespacedName{Namespace: namespace, Name: reg.AuthSecretRef.Name}
		if err := c.Get(ctx, key, &secret); err != nil {
			return nil, fmt.Errorf("resolving registry auth for %q: getting Secret %q in the controller's own namespace: %w",
				reg.Host, key, err)
		}

		var auth RegistryAuth

		if v, ok := secret.Data[registryAuthPasswordKey]; ok {
			auth.Password = string(v)
		}

		if v, ok := secret.Data[registryAuthAuthKey]; ok {
			auth.Auth = string(v)
		}

		if v, ok := secret.Data[registryAuthIdentityTokenKey]; ok {
			auth.IdentityToken = string(v)
		}

		if auth.Password == "" && auth.Auth == "" && auth.IdentityToken == "" {
			return nil, fmt.Errorf("resolving registry auth for %q: Secret %q has none of the keys %q, %q, %q",
				reg.Host, key, registryAuthPasswordKey, registryAuthAuthKey, registryAuthIdentityTokenKey)
		}

		resolved[reg.Host] = auth
	}

	return resolved, nil
}

// injectRegistries merges cluster.Spec.Registries' mirror endpoints and auth's resolved
// credentials into cfg, directly through machinery's own v1alpha1.Config type rather than
// through a TalosConfigPatch document and its mirror types: the password-shaped fields have no
// mirror-type representation at all (that is the entire reason authSecretRef exists), so there
// is no typedconfig document kind this could go through. The merge uses the same
// configpatcher.Apply strategic merge every TalosConfigPatch already goes through, so later
// layers (applied afterwards by the caller) compose with it exactly like any other document
// rather than needing a special case.
//
// A registry with no entry in auth (anonymous, or auth resolution wasn't run at all -- auth may
// be nil) still gets its mirror endpoints; only RegistryConfig/.auth is conditional on there
// being a username or resolved credential to put there.
func injectRegistries(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster, auth map[string]RegistryAuth,
) (coreconfig.Provider, error) {
	if len(cluster.Spec.Registries) == 0 {
		return cfg, nil
	}

	mirrors := make(map[string]*mv1alpha1.RegistryMirrorConfig, len(cluster.Spec.Registries))
	regConfig := make(map[string]*mv1alpha1.RegistryConfig, len(cluster.Spec.Registries))

	for _, reg := range cluster.Spec.Registries {
		mirrors[reg.Host] = &mv1alpha1.RegistryMirrorConfig{MirrorEndpoints: reg.Endpoints}

		a, hasAuth := auth[reg.Host]
		if !hasAuth && reg.Username == "" {
			continue
		}

		regConfig[reg.Host] = &mv1alpha1.RegistryConfig{
			RegistryAuth: &mv1alpha1.RegistryAuthConfig{
				RegistryUsername:      reg.Username,
				RegistryPassword:      a.Password,
				RegistryAuth:          a.Auth,
				RegistryIdentityToken: a.IdentityToken,
			},
		}
	}

	doc := &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{
			// MachineRegistries is deprecated in favor of the standalone
			// RegistryMirrorConfig/RegistryAuthConfig documents (package
			// config/types/cri), but those are a different document kind, not
			// in Ichor's allow-list (DocumentFromPatch's switch): adding them
			// is a mirrorgen-root-sized change, out of
			// scope for this render-time injection. Using the legacy nested
			// field keeps this consistent with every other document Ichor
			// renders today.
			MachineRegistries: mv1alpha1.RegistriesConfig{ //nolint:staticcheck // see comment above
				RegistryMirrors: mirrors,
				RegistryConfig:  regConfig,
			},
		},
	}

	c, err := container.New(doc)
	if err != nil {
		return nil, fmt.Errorf("render: building registries document: %w", err)
	}

	patch := configpatcher.NewStrategicMergePatch(c)

	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg), []configpatcher.Patch{patch})
	if err != nil {
		return nil, fmt.Errorf("render: injecting registry mirrors: %w", err)
	}

	return out.Config()
}
