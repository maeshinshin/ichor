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

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestNewKubeClientForConfig_RejectsUnknownField proves architecture.md's
// "Typed configuration" decision -- no admission webhook for this, Ichor's
// own client is strict instead -- actually fires. A plain, non-strict
// client's Create silently prunes a field the API server's schema doesn't
// recognize (confirmed separately: no error, just a dropped value and a
// warning header nothing here reads), which is exactly how a stale field
// name -- e.g. one of the nine secret fields the Config mirror type
// dropped -- could vanish into a TalosConfigPatch
// with no sign anything was wrong. This test builds a TalosConfigPatch as
// unstructured.Unstructured with a `token` field injected into its
// machine config (a field the current CRD schema no longer has at all)
// and requires the real API server strict validation gives to reject it
// by name, not silently accept it.
//
// Needs a real API server (KUBEBUILDER_ASSETS from `make test`'s envtest
// setup): fake clients don't model server-side schema pruning, so this is
// the one place in this package that cannot be proven against a fake.
func TestNewKubeClientForConfig_RejectsUnknownField(t *testing.T) {
	assetsDir := os.Getenv("KUBEBUILDER_ASSETS")
	if assetsDir == "" {
		assetsDir = firstFoundEnvTestBinaryDir(t)
	}

	if assetsDir == "" {
		t.Skip("no envtest binaries found (set KUBEBUILDER_ASSETS or run via `make test`)")
	}

	// envtest's wait for the "default" namespace is a fixed 5s that cannot
	// be configured; under load (a parallel full gate) it can miss, so retry
	// the whole start a bounded number of times.
	var (
		env *envtest.Environment
		cfg *rest.Config
		err error
	)

	for attempt := 1; attempt <= 3; attempt++ {
		env = &envtest.Environment{
			CRDDirectoryPaths:        []string{filepath.Join("..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing:    true,
			BinaryAssetsDirectory:    assetsDir,
			ControlPlaneStartTimeout: time.Minute,
		}

		cfg, err = env.Start()
		if err == nil {
			break
		}

		t.Logf("starting envtest (attempt %d/3): %v", attempt, err)

		_ = env.Stop()
	}

	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}

	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stopping envtest: %v", err)
		}
	})

	k8sClient, err := newKubeClientForConfig(cfg)
	if err != nil {
		t.Fatalf("newKubeClientForConfig: %v", err)
	}

	patch := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": testAPIVersion,
		"kind":       "TalosConfigPatch",
		"metadata": map[string]any{
			"name":      "stale-field",
			"namespace": testNamespace,
		},
		"spec": map[string]any{
			"clusterRef": map[string]any{"name": "some-cluster"},
			"layer":      cniPatchLayer,
			"documents": []any{
				map[string]any{
					"kind": "Config",
					"config": map[string]any{
						"version": "v1alpha1",
						"machine": map[string]any{
							"type":     testRoleWorker,
							"certSANs": []any{"10.0.0.10"},
							// "token" no longer exists on MachineConfig; a strict client must reject
							// it by name, not drop it silently.
							"token": "stale-value-that-must-not-silently-vanish",
						},
						"cluster": map[string]any{},
					},
				},
			},
		},
	}}

	err = k8sClient.Create(context.Background(), patch)
	if err == nil {
		t.Fatal("expected strict field validation to reject the unknown \"token\" field, got no error")
	}

	if !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected the error to name the unknown field \"token\", got: %v", err)
	}
}

// firstFoundEnvTestBinaryDir mirrors internal/controller/suite_test.go's
// getFirstFoundEnvTestBinaryDir: locates the first binary directory under
// bin/k8s, for runs that didn't get KUBEBUILDER_ASSETS set by `make test`
// (e.g. an IDE's own test runner).
func firstFoundEnvTestBinaryDir(t *testing.T) string {
	t.Helper()

	basePath := filepath.Join("..", "..", "bin", "k8s")

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}

	return ""
}
