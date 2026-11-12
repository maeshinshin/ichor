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
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

// bundleMaterialFromHome reads back the secrets bundle runInit just saved
// to $HOME/.ichor/clusters/<cluster>/secrets.yaml, for routes where the
// bundle is known to have been generated and written before the failure
// this subtest forces.
func bundleMaterialFromHome(t *testing.T) []string {
	t.Helper()

	path, err := bundleDir(testInitClusterName)
	if err != nil {
		t.Fatalf("bundleDir: %v", err)
	}

	bundle, err := loadBundleFile(path + "/" + secretsbundle.SecretKey)
	if err != nil {
		t.Fatalf("loadBundleFile: %v", err)
	}

	return bundleSecretMaterial(bundle)
}

// onRead calls f when the prompt first reads its answer.
type onRead struct {
	io.Reader
	f func()
}

func (r onRead) Read(p []byte) (int, error) {
	r.f()

	return r.Reader.Read(p)
}

// TestInitNeverLeaksSecrets forces nine routes through runInit --
// including the two that succeed and actually create CRs and print
// output -- and asserts that none of the generated bundle's secret
// material ever appears in stdout, stderr or an error message. Same
// discipline as pkg/render's, internal/controller's and adopt's own leak
// tests (internal/cli/leak_test.go): this is the one command besides
// adopt that holds a real secrets.Bundle in memory.
func TestInitNeverLeaksSecrets(t *testing.T) {
	assertNoLeak := func(t *testing.T, material []string, stdout, stderr string, err error) {
		t.Helper()

		haystacks := []string{stdout, stderr}
		if err != nil {
			haystacks = append(haystacks, err.Error())
		}

		for _, s := range material {
			if s == "" {
				continue
			}

			for _, h := range haystacks {
				if strings.Contains(h, s) {
					t.Fatalf("output contains bundle secret material:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
				}
			}
		}
	}

	run := func(t *testing.T, mutateOpts func(*initOptions), mutateDeps func(*initDeps)) (string, string, error) {
		t.Helper()

		withTempHome(t)

		k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
		deps, _, _ := newFakeInitDeps(t, k8sClient)

		var stdout, stderr bytes.Buffer

		deps.Stdout, deps.Stderr = &stdout, &stderr

		opts := validInitOptions()
		if mutateOpts != nil {
			mutateOpts(&opts)
		}

		if mutateDeps != nil {
			mutateDeps(&deps)
		}

		err := runInit(context.Background(), opts, deps)

		return stdout.String(), stderr.String(), err
	}

	t.Run("identity read failed (before bundle exists)", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			d.ReadNode = func(context.Context, string, bool) (nodeReading, error) {
				return nodeReading{}, errors.New("dial failed")
			}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, nil, stdout, stderr, err)
	})

	t.Run("invalid version contract (before bundle exists)", func(t *testing.T) {
		stdout, stderr, err := run(t, func(o *initOptions) { o.VersionContract = "not-a-version" }, nil)

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, nil, stdout, stderr, err)
	})

	// A run that fails before the node is sent anything removes its bundle file, so these two read the
	// bundle at the moment of the failure.
	t.Run("maintenance dial failed", func(t *testing.T) {
		var material []string

		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			d.DialMaintenance = func(context.Context, string) (maintenanceApplier, error) {
				material = bundleMaterialFromHome(t)

				return nil, errors.New("connection refused")
			}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, material, stdout, stderr, err)
	})

	t.Run("insecure apply failed", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			maint := &fakeMaintenanceApplier{applyErr: errors.New("apply failed")}
			d.DialMaintenance = func(context.Context, string) (maintenanceApplier, error) { return maint, nil }
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})

	t.Run("admin dial failed", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			d.DialAdmin = func(context.Context, *ichorv1alpha1.TalosCluster, string, *secrets.Bundle) (bootstrapClient, error) {
				return nil, errors.New("connection refused")
			}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})

	t.Run("bootstrap failed", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			boot := &fakeBootstrapClient{bootstrapErr: errors.New("bootstrap failed")}
			d.DialAdmin = func(context.Context, *ichorv1alpha1.TalosCluster, string, *secrets.Bundle) (bootstrapClient, error) {
				return boot, nil
			}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})

	t.Run("kubeconfig fetch failed", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			boot := &fakeBootstrapClient{kubeconfigErr: errors.New("fetch failed")}
			d.DialAdmin = func(context.Context, *ichorv1alpha1.TalosCluster, string, *secrets.Bundle) (bootstrapClient, error) {
				return boot, nil
			}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})

	t.Run("confirmation declined", func(t *testing.T) {
		var material []string

		stdout, stderr, err := run(t, nil, func(d *initDeps) {
			d.AssumeYes = false
			d.Stdin = onRead{strings.NewReader("n\n"), func() { material = bundleMaterialFromHome(t) }}
		})

		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		assertNoLeak(t, material, stdout, stderr, err)
	})

	t.Run("successful run (yaml output)", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, nil)
		if err != nil {
			t.Fatalf("runInit: %v", err)
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})

	t.Run("successful run (json output)", func(t *testing.T) {
		stdout, stderr, err := run(t, nil, func(d *initDeps) { d.Output = outputJSON })
		if err != nil {
			t.Fatalf("runInit: %v", err)
		}

		assertNoLeak(t, bundleMaterialFromHome(t), stdout, stderr, err)
	})
}
