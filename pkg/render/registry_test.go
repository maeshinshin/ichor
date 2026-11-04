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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	registrySecretNamespace = "ichor-system"
	registryHost            = "registry.example.com"
	registrySecretName      = "registry-creds"
	registryMirrorEndpoint  = "https://mirror.example.com"
)

func registryCluster(entries ...ichorv1alpha1.TalosClusterRegistry) *ichorv1alpha1.TalosCluster {
	c := testCluster("true")
	c.Spec.Registries = entries

	return c
}

// authRegistryCluster is registryCluster with one mirror whose credentials are in a Secret.
func authRegistryCluster() *ichorv1alpha1.TalosCluster {
	return registryCluster(ichorv1alpha1.TalosClusterRegistry{
		Host: registryHost, Endpoints: []string{registryMirrorEndpoint},
		AuthSecretRef: &corev1.LocalObjectReference{Name: registrySecretName},
	})
}

// TestResolveRegistryAuth checks every outcome ResolveRegistryAuth's own doc comment promises:
// an anonymous registry is skipped, a resolvable one lands in the returned map, and a registry
// that names a Secret fails loudly -- never silently -- when that Secret is missing or carries
// none of the three recognized keys.
func TestResolveRegistryAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("no registries at all resolves to an empty map", func(t *testing.T) {
		c := registryCluster()
		k8sClient := fake.NewClientBuilder().Build()

		got, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, c)
		if err != nil {
			t.Fatalf("ResolveRegistryAuth: %v", err)
		}

		if len(got) != 0 {
			t.Fatalf("expected an empty map, got %v", got)
		}
	})

	t.Run("a registry with no authSecretRef is anonymous, not an error", func(t *testing.T) {
		c := registryCluster(ichorv1alpha1.TalosClusterRegistry{
			Host: registryHost, Endpoints: []string{registryMirrorEndpoint},
		})
		k8sClient := fake.NewClientBuilder().Build()

		got, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, c)
		if err != nil {
			t.Fatalf("ResolveRegistryAuth: %v", err)
		}

		if _, ok := got[registryHost]; ok {
			t.Fatalf("expected no entry for an anonymous registry, got %v", got[registryHost])
		}
	})

	t.Run("resolves password, auth and identityToken independently", func(t *testing.T) {
		cases := []struct {
			name string
			data map[string][]byte
			want RegistryAuth
		}{
			{"password", map[string][]byte{"password": []byte("s3cr3t-pw")}, RegistryAuth{Password: "s3cr3t-pw"}},
			{"auth", map[string][]byte{"auth": []byte("dXNlcjpwYXNz")}, RegistryAuth{Auth: "dXNlcjpwYXNz"}},
			{"identityToken", map[string][]byte{"identityToken": []byte("tok-abc")}, RegistryAuth{IdentityToken: "tok-abc"}},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				secret := &corev1.Secret{
					Namespace: registrySecretNamespace, Name: registrySecretName,
					Data: tc.data,
				}
				c := authRegistryCluster()
				k8sClient := fake.NewClientBuilder().WithObjects(secret).Build()

				got, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, c)
				if err != nil {
					t.Fatalf("ResolveRegistryAuth: %v", err)
				}

				if got[registryHost] != tc.want {
					t.Fatalf("got %+v, want %+v", got[registryHost], tc.want)
				}
			})
		}
	})

	t.Run("fails explicitly when the Secret is missing", func(t *testing.T) {
		c := authRegistryCluster()
		k8sClient := fake.NewClientBuilder().Build()

		_, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, c)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), registryHost) || !strings.Contains(err.Error(), registrySecretName) {
			t.Fatalf("expected the error to name the registry and the Secret, got: %v", err)
		}
	})

	t.Run("fails explicitly when the Secret has none of the recognized keys", func(t *testing.T) {
		secret := &corev1.Secret{
			Namespace: registrySecretNamespace, Name: registrySecretName,
			Data: map[string][]byte{"not-a-recognized-key": []byte("whatever")},
		}
		c := authRegistryCluster()
		k8sClient := fake.NewClientBuilder().WithObjects(secret).Build()

		_, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, c)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), registryHost) {
			t.Fatalf("expected the error to name the registry, got: %v", err)
		}
	})

	t.Run("fails explicitly, without touching the API, when the controller namespace is empty", func(t *testing.T) {
		c := authRegistryCluster()
		k8sClient := fake.NewClientBuilder().Build()

		_, err := ResolveRegistryAuth(ctx, k8sClient, "", c)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "controller namespace is empty") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestRenderRegistries checks that Render actually injects what
// ResolveRegistryAuth resolved -- mirrors, username and the resolved credential all land in the
// rendered config -- and that the injection is the system layer (ADR 0027): a cluster-layer
// TalosConfigPatch that also sets machine.registries.mirrors for the same host still wins.
func TestRenderRegistries(t *testing.T) {
	cluster := registryCluster(ichorv1alpha1.TalosClusterRegistry{
		Host:      registryHost,
		Endpoints: []string{registryMirrorEndpoint},
		Username:  "deploy",
	})
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)
	auth := map[string]RegistryAuth{registryHost: {Password: "s3cr3t-pw"}}

	out, _, err := Render(cluster, m, bundle, nil, &SystemInputs{RegistryAuth: auth})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	requireAll(t, string(out), registryHost, "mirror.example.com", "username: deploy", "password: s3cr3t-pw")

	// System layer (ADR 0027): a cluster-layer TalosConfigPatch that also sets
	// machine.registries must still win over what injectRegistries put there, same as any
	// other layer/patch ordering render.go already guarantees. username is a scalar field --
	// machinery's own merge rules (config/merge/merge.go) replace a scalar with whatever the
	// later side sets, unlike a []string field (RegistryMirrorConfig.endpoints), which Talos
	// merges by concatenating rather than replacing; that is Talos's own semantics for every
	// list-typed config field, not something this feature changes, so it is not what this test
	// exercises.
	t.Run("a cluster-layer patch overrides the system layer's username", func(t *testing.T) {
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "override-username",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerCluster,
				Documents: []ichorv1alpha1.TalosConfigPatchDocument{
					{
						Kind: kindConfig,
						Config: &typedconfig.Config{
							ConfigVersion: configVersionV1Alpha1,
							MachineConfig: &typedconfig.MachineConfig{
								MachineRegistries: &typedconfig.RegistriesConfig{
									RegistryConfig: map[string]*typedconfig.RegistryConfig{
										registryHost: {RegistryAuth: &typedconfig.RegistryAuthConfig{RegistryUsername: "overridden-user"}},
									},
								},
							},
						},
					},
				},
			},
		}

		out, _, err := Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, &SystemInputs{RegistryAuth: auth})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		// The credential the patch never touched must survive the override.
		requireAll(t, string(out), "username: overridden-user", "password: s3cr3t-pw")
		requireNone(t, string(out), "username: deploy")
	})
}

// TestResolveRegistryAuthNoRegistries checks that a cluster with no registries resolves to an
// empty map without reading anything (the client has no objects, and the namespace may even be
// empty: nothing is looked up), and that rendering with it is identical to rendering with nil.
func TestResolveRegistryAuthNoRegistries(t *testing.T) {
	cluster := testCluster("true")
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)

	k8sClient := fake.NewClientBuilder().Build()

	auth, err := ResolveRegistryAuth(context.Background(), k8sClient, "", cluster)
	if err != nil {
		t.Fatalf("ResolveRegistryAuth: %v", err)
	}

	if len(auth) != 0 {
		t.Fatalf("expected no resolved auth, got %d entries", len(auth))
	}

	plain, plainHash, err := Render(cluster, m, bundle, nil, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	withAuth, withAuthHash, err := Render(cluster, m, bundle, nil, &SystemInputs{RegistryAuth: auth})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if string(plain) != string(withAuth) || plainHash != withAuthHash {
		t.Fatalf("expected identical output with no registries declared")
	}
}

// TestRegistryAuthLeak mirrors TestRenderBundleLeak's discipline for this package's second
// secret-bearing input: no error ResolveRegistryAuth or Render can return, on any
// path this test can force, may contain the registry's resolved credential.
func TestRegistryAuthLeak(t *testing.T) {
	const secretValue = "leak-canary-s3cr3t-pw-should-never-appear"

	ctx := context.Background()
	cluster := authRegistryCluster()

	assertNoLeak := func(t *testing.T, err error) {
		t.Helper()

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if strings.Contains(err.Error(), secretValue) {
			t.Fatalf("error message contains the registry secret value: %v", err)
		}
	}

	t.Run("Secret missing", func(t *testing.T) {
		k8sClient := fake.NewClientBuilder().Build()
		_, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, cluster)
		assertNoLeak(t, err)
	})

	t.Run("Secret has none of the recognized keys, value present but under the wrong key", func(t *testing.T) {
		secret := &corev1.Secret{
			Namespace: registrySecretNamespace, Name: registrySecretName,
			Data: map[string][]byte{"wrong-key": []byte(secretValue)},
		}
		k8sClient := fake.NewClientBuilder().WithObjects(secret).Build()
		_, err := ResolveRegistryAuth(ctx, k8sClient, registrySecretNamespace, cluster)
		assertNoLeak(t, err)
	})

	// This forces Render to fail with a real credential
	// already resolved (auth), but not inside injectRegistries itself: an
	// invalid versionContract fails earlier, at buildConfig's own
	// coreconfig.ParseContractFromVersion call (render.go), before
	// injectRegistries -- and the real credential it carries -- is ever
	// reached. What this test file could not force, for the same reason
	// TestRenderBundleLeak names for its own two unreachable paths: either
	// of injectRegistries's own two error returns (registry.go).
	// container.New(doc) is called with exactly one, already-typed
	// in-process Go document, so its only failure modes (duplicate or
	// conflicting document) cannot trigger against itself.
	// configpatcher.Apply merges that same document onto cfg, a config
	// baseConfig just generated fresh via machinery's own generator, so
	// there is no reachable way through this package's public API to make
	// either call actually fail -- confirmed by reading both
	// (pkg/machinery/config/container/container.go's New,
	// pkg/machinery/config/configpatcher's Apply) rather than assumed.
	t.Run("render fails before registries are ever touched (invalid versionContract)", func(t *testing.T) {
		m := testMachine("node-a", roleWorker, nil)
		bundle := testBundle(t)
		auth := map[string]RegistryAuth{registryHost: {Password: secretValue}}

		bad := registryCluster(cluster.Spec.Registries...)
		bad.Spec.VersionContract = "not-a-version"

		_, _, err := Render(bad, m, bundle, nil, &SystemInputs{RegistryAuth: auth})
		assertNoLeak(t, err)
	})
}
