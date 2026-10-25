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

package gitops

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// skeletonAppNames are the three apps every testSkeleton builds, named so
// goconst doesn't flag the repeated literals.
const (
	skeletonAppArgoCD = "argocd"
	skeletonAppCNI    = "cni"
	skeletonAppIchor  = "ichor"
)

func mustApp(t *testing.T, opts Options) *Application {
	t.Helper()

	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return app
}

func testSkeleton(t *testing.T) Skeleton {
	t.Helper()

	root := mustApp(t, Options{
		Name: "root",
		Source: &ApplicationSource{
			RepoURL:   testRepoURL,
			Path:      "apps",
			Directory: &ApplicationSourceDir{Recurse: true},
		},
		Destination: ApplicationDestination{Server: testServer, Namespace: skeletonAppArgoCD},
	})

	appOf := func(name string, wave int) *Application {
		return mustApp(t, Options{
			Name: name,
			Source: &ApplicationSource{
				RepoURL: testRepoURL,
				Path:    "apps/" + name,
			},
			Destination: ApplicationDestination{Server: testServer, Namespace: name},
			SyncWave:    wave,
		})
	}

	return Skeleton{
		Cluster: "prod",
		RootApp: root,
		Apps: map[string]*Application{
			skeletonAppArgoCD: appOf(skeletonAppArgoCD, SyncWaveArgoCD),
			skeletonAppCNI:    appOf(skeletonAppCNI, SyncWaveCNI),
			skeletonAppIchor:  appOf(skeletonAppIchor, SyncWaveIchor),
		},
		CRs: map[string]any{
			"talos-cluster.yaml": map[string]any{
				"apiVersion": "ichor.maesh.dev/v1alpha1",
				"kind":       "TalosCluster",
				"metadata":   map[string]any{"name": "prod"},
			},
			"machine-node1.yaml": map[string]any{
				"apiVersion": "ichor.maesh.dev/v1alpha1",
				"kind":       "TalosMachine",
				"metadata":   map[string]any{"name": "node1"},
			},
		},
	}
}

// walkFiles returns every regular file under dir, relative to dir, sorted.
func walkFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		files[rel] = b

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}

	return files
}

// TestSkeleton_Write_LayoutMatchesDesign checks the fixed directory
// structure from the design doc exists with the expected files.
func TestSkeleton_Write_LayoutMatchesDesign(t *testing.T) {
	dir := t.TempDir()

	if err := testSkeleton(t).Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	want := []string{
		filepath.Join("bootstrap", "root-app.yaml"),
		filepath.Join("apps", skeletonAppArgoCD, "application.yaml"),
		filepath.Join("apps", skeletonAppCNI, "application.yaml"),
		filepath.Join("apps", skeletonAppIchor, "application.yaml"),
		filepath.Join("clusters", "prod", "talos-cluster.yaml"),
		filepath.Join("clusters", "prod", "machine-node1.yaml"),
	}

	for _, rel := range want {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected file %s: %v", rel, err)
		}
	}
}

// TestSkeleton_Write_Deterministic is the determinism acceptance criterion:
// the same Skeleton value, written twice, produces byte-for-byte identical
// output despite Go's map iteration being unordered.
func TestSkeleton_Write_Deterministic(t *testing.T) {
	sk := testSkeleton(t)

	dirA := t.TempDir()
	dirB := t.TempDir()

	if err := sk.Write(dirA); err != nil {
		t.Fatalf("Write(dirA): %v", err)
	}

	if err := sk.Write(dirB); err != nil {
		t.Fatalf("Write(dirB): %v", err)
	}

	filesA := walkFiles(t, dirA)
	filesB := walkFiles(t, dirB)

	if len(filesA) != len(filesB) {
		t.Fatalf("file count differs: %d vs %d", len(filesA), len(filesB))
	}

	for rel, a := range filesA {
		b, ok := filesB[rel]
		if !ok {
			t.Fatalf("%s present in dirA but not dirB", rel)
		}

		if string(a) != string(b) {
			t.Errorf("%s differs between runs:\n--- A ---\n%s\n--- B ---\n%s", rel, a, b)
		}
	}

	// And repeated marshaling of the map-keyed parts (Apps, CRs) alone is
	// stable across many attempts, not just two -- Go deliberately
	// randomizes map iteration per run, so a flake here would show up only
	// probabilistically.
	for i := range 20 {
		dir := t.TempDir()
		if err := sk.Write(dir); err != nil {
			t.Fatalf("Write (iteration %d): %v", i, err)
		}

		files := walkFiles(t, dir)
		for rel, want := range filesA {
			if got := string(files[rel]); got != string(want) {
				t.Fatalf("iteration %d: %s differs from the first run", i, rel)
			}
		}
	}
}

// TestSkeleton_Write_RefusesSecret is the credential/secret leak test: a
// Secret must never be written anywhere under the skeleton, however it
// arrives in CRs.
func TestSkeleton_Write_RefusesSecret(t *testing.T) {
	sk := testSkeleton(t)
	sk.CRs["sneaky-secret.yaml"] = &corev1.Secret{
		Name:       "repo-creds",
		StringData: map[string]string{"password": "supersecret"},
	}

	dir := t.TempDir()

	err := sk.Write(dir)
	if err == nil {
		t.Fatal("Write: want error when a CR is a Secret, got nil")
	}

	if !strings.Contains(err.Error(), "Secret") {
		t.Errorf("error = %q, want it to mention Secret", err)
	}

	// Nothing should have been written for that entry, and the rest of
	// the tree should not contain the secret value either.
	for rel, content := range walkFiles(t, dir) {
		if strings.Contains(string(content), "supersecret") {
			t.Errorf("%s contains the secret value", rel)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "clusters", "prod", "sneaky-secret.yaml")); err == nil {
		t.Error("sneaky-secret.yaml was written despite being a Secret")
	}
}

// TestSkeleton_Write_RefusesCredentialedRepoURL is the credential-leak test
// at the skeleton level: an Application that reached Skeleton without going
// through New (so New's own check never ran) is still caught before
// anything is written.
func TestSkeleton_Write_RefusesCredentialedRepoURL(t *testing.T) {
	sk := testSkeleton(t)
	sk.Apps[skeletonAppCNI] = &Application{
		Name: skeletonAppCNI,
		Spec: ApplicationSpec{
			Project: defaultProject,
			Source: &ApplicationSource{
				RepoURL: "https://x-access-token:ghp_leaked@github.com/org/repo.git",
				Path:    "apps/cni",
			},
			Destination: ApplicationDestination{Server: testServer},
		},
	}

	dir := t.TempDir()

	err := sk.Write(dir)
	if err == nil {
		t.Fatal("Write: want error for a credentialed repoURL, got nil")
	}

	for rel, content := range walkFiles(t, dir) {
		if strings.Contains(string(content), "ghp_leaked") {
			t.Errorf("%s contains the leaked token", rel)
		}
	}
}

// TestSkeleton_Write_RequiresClusterName checks the one required field on
// Skeleton itself.
func TestSkeleton_Write_RequiresClusterName(t *testing.T) {
	sk := testSkeleton(t)
	sk.Cluster = ""

	if err := sk.Write(t.TempDir()); err == nil {
		t.Fatal("Write: want error for empty Cluster, got nil")
	}
}

// TestSortedKeys checks the determinism helper directly: same map, same
// order, every time, sorted.
func TestSortedKeys(t *testing.T) {
	m := map[string]int{"zebra": 1, "mango": 2, "kiwi": 3, "apple": 4}

	got := sortedKeys(m)

	want := []string{"apple", "kiwi", "mango", "zebra"}
	if !slices.IsSorted(got) || len(got) != len(want) {
		t.Fatalf("sortedKeys = %v, want sorted %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedKeys = %v, want %v", got, want)
		}
	}
}
