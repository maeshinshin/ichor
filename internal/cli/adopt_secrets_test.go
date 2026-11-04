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
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"

	"github.com/maeshinshin/ichor/pkg/render"
)

const testControlPlaneRole = "controlplane"

func renderRole(t *testing.T, role string) []byte {
	t.Helper()

	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = role

	cfg, _, err := render.Render(cluster, machine, newTestBundle(t), nil, nil)
	if err != nil {
		t.Fatalf("rendering %s fixture: %v", role, err)
	}

	return cfg
}

func TestRecoverSecretsBundle_RoundTrip(t *testing.T) {
	want := newTestBundle(t)
	reader := &fakeTalosReader{current: renderRole(t, testControlPlaneRole)}

	contract, err := parseAdoptContract(testVersionContract)
	if err != nil {
		t.Fatal(err)
	}

	got, err := recoverSecretsBundle(context.Background(), reader, contract)
	if err != nil {
		t.Fatalf("recoverSecretsBundle: %v", err)
	}

	wantYAML, _ := yaml.Marshal(want)
	gotYAML, _ := yaml.Marshal(got)

	if !bytes.Equal(wantYAML, gotYAML) {
		t.Fatal("recovered bundle differs from the one the config was rendered from")
	}
}

func TestRecoverSecretsBundle_Refusals(t *testing.T) {
	contract, _ := parseAdoptContract(testVersionContract)
	marker := "LEAKMARKER-0123456789"

	cases := map[string]struct {
		reader  *fakeTalosReader
		wantErr error
		noLeak  string
	}{
		"worker config": {reader: &fakeTalosReader{current: renderRole(t, testRoleWorker)}, wantErr: errNotControlPlane},
		"unparseable":   {reader: &fakeTalosReader{current: []byte("version: v1alpha1\nmachine: {token: " + marker + "\n  [")}, noLeak: marker},
		"read error":    {reader: &fakeTalosReader{currentErr: errors.New("boom")}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bundle, err := recoverSecretsBundle(context.Background(), tc.reader, contract)
			if err == nil || bundle != nil {
				t.Fatalf("want an error and no bundle, got bundle=%v err=%v", bundle, err)
			}

			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}

			if tc.noLeak != "" && strings.Contains(err.Error(), tc.noLeak) {
				t.Fatalf("error leaks config content: %v", err)
			}
		})
	}
}

func TestResolveAdoptBundle_RecoverStoresPrivately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	reader := &fakeTalosReader{current: renderRole(t, testControlPlaneRole)}

	var stderr bytes.Buffer

	opts := adoptSecretsOptions{Recover: true, RecoverFrom: "10.0.0.1:50000", Cluster: "c1", Contract: testVersionContract, Plaintext: true}

	bundle, save, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr)
	if err != nil || bundle == nil {
		t.Fatalf("resolveAdoptBundle: %v", err)
	}

	dir, err := bundleDir("c1")
	if err != nil {
		t.Fatal(err)
	}

	// Resolving writes nothing: the plan phase leaves no file (ADR 0103, 1.8).
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("resolveAdoptBundle left something on disk: %v", err)
	}

	if err := save(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "secrets.yaml")

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}

	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	// Reloads as a file, and nothing secret went to the terminal.
	if _, err := loadBundleFile(path); err != nil {
		t.Fatalf("stored bundle does not reload: %v", err)
	}

	if strings.Contains(stderr.String(), bundle.TrustdInfo.Token) || !strings.Contains(stderr.String(), path) {
		t.Fatalf("stderr should name the path and nothing else: %q", stderr.String())
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A rerun that recovers the same bundle (the first run was killed after saving) is accepted
	// and leaves the file alone.
	again, saveAgain, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr)
	if err != nil || again == nil || saveAgain == nil {
		t.Fatalf("rerun with the identical bundle: %v", err)
	}

	if err := saveAgain(); err != nil {
		t.Fatal(err)
	}

	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("the rerun changed the stored bundle")
	}

	// A different recovered bundle is never allowed to overwrite it.
	if err := os.WriteFile(path, append(before, []byte("# another cluster's\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveAdoptBundle(context.Background(), opts, dialerFor(reader, nil), &stderr); !errors.Is(err, errRecoveredExists) {
		t.Fatalf("different bundle err = %v, want errRecoveredExists", err)
	}
}

func TestResolveAdoptBundle_FlagCombinations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dial := dialerFor(&fakeTalosReader{}, nil)

	for name, tc := range map[string]struct {
		opts adoptSecretsOptions
		want error
	}{
		"neither":         {adoptSecretsOptions{}, errNoSecretsSource},
		"both":            {adoptSecretsOptions{File: "x", Recover: true}, errBothSecretsSource},
		"no recover-from": {adoptSecretsOptions{Recover: true, Cluster: "c"}, errNoRecoverFrom},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := resolveAdoptBundle(context.Background(), tc.opts, dial, &bytes.Buffer{}); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestResolveAdoptBundle_FileStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yaml")

	raw, err := yaml.Marshal(newTestBundle(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	bundle, stored, err := resolveAdoptBundle(context.Background(), adoptSecretsOptions{File: path}, nil, &bytes.Buffer{})
	if err != nil || bundle == nil || stored != nil {
		t.Fatalf("bundle=%v stored=%t err=%v", bundle, stored != nil, err)
	}
}
