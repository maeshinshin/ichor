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

package installer

import (
	"context"
	"os"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

func loadCRD(t *testing.T, path string) *unstructured.Unstructured {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	crd := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(raw, &crd.Object); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	return crd
}

// A TalosCluster stored under the schema that still had spec.gitops (and spec.cni.handover) must
// stay writable once the current CRD, which no longer has them, is installed (ADR 0101): the
// installer's CRD check allows the removal, the API server prunes the retired fields and no
// validation rejects the object for carrying them.
func TestStoredTalosClusterWithRetiredFieldsSurvivesTheCRDUpgrade(t *testing.T) {
	ctx := context.Background()

	// testdata is the TalosCluster CRD as it was before the removal (git show c7902de:config/crd/bases/...).
	old := loadCRD(t, "testdata/talosclusters-before-adr0101.yaml")
	if err := k8sClient.Create(ctx, old); err != nil {
		t.Fatalf("installing the old CRD: %v", err)
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: "ichor.maesh.dev/v1alpha1",
		kKind:       "TalosCluster",
		kMetadata:   map[string]any{kName: "old", "namespace": "default"},
		kSpec: map[string]any{
			"endpoint":          map[string]any{kType: "vip", "address": "10.0.0.1"},
			"talosVersion":      "v1.14.3",
			"kubernetesVersion": "v1.36.0",
			"versionContract":   "v1.14",
			"secretsRef":        map[string]any{kName: "secrets"},
			"controlPlane":      map[string]any{"count": int64(3), "schedulable": "true"},
			"cni":               map[string]any{kHandover: "manual"},
			kGitops: map[string]any{
				"mode": "strict",
				"repository": map[string]any{
					"host": "github", "apiURL": "https://api.github.com", "owner": "acme", "repo": "infra",
					"cloneURL":       "https://github.com/acme/infra.git",
					"tokenSecretRef": map[string]any{kName: "git-token", "key": "pat"},
					"machinesPath":   "machines",
				},
			},
		},
	}}

	// The CRD is established asynchronously.
	var err error

	for range 50 {
		if err = k8sClient.Create(ctx, obj); err == nil {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("creating the TalosCluster under the old schema: %v", err)
	}

	if _, found, _ := unstructured.NestedMap(obj.Object, kSpec, kGitops); !found {
		t.Fatal("the old schema did not keep spec.gitops")
	}

	live := &unstructured.Unstructured{}
	live.SetAPIVersion("apiextensions.k8s.io/v1")
	live.SetKind(kindCRD)

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: old.GetName()}, live); err != nil {
		t.Fatal(err)
	}

	target := loadCRD(t, "../../config/crd/bases/ichor.maesh.dev_talosclusters.yaml")
	if err := checkAdditive(live, target); err != nil {
		t.Fatalf("the installer would refuse the upgrade: %v", err)
	}

	target.SetResourceVersion(live.GetResourceVersion())

	if err := k8sClient.Update(ctx, target); err != nil {
		t.Fatalf("installing the new CRD: %v", err)
	}

	// Update the stored object, changing its spec.
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "old"}, obj); err != nil {
		t.Fatal(err)
	}

	if err := unstructured.SetNestedField(obj.Object, "10.0.0.2", kSpec, "endpoint", "address"); err != nil {
		t.Fatal(err)
	}

	if err := k8sClient.Update(ctx, obj); err != nil {
		t.Fatalf("updating the stored TalosCluster under the new CRD: %v", err)
	}

	for _, path := range [][]string{{kSpec, kGitops}, {kSpec, "cni", kHandover}} {
		if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, path...); found {
			t.Errorf("%v survived the update: the API server should prune it", path)
		}
	}
}

// The same for IchorInstallation: spec.managementMode, spec.stack and status.stack are retired.
func TestStoredIchorInstallationWithRetiredFieldsSurvivesTheCRDUpgrade(t *testing.T) {
	ctx := context.Background()

	old := loadCRD(t, "testdata/ichorinstallations-before-adr0101.yaml")
	if err := k8sClient.Create(ctx, old); err != nil {
		t.Fatalf("installing the old CRD: %v", err)
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: "ichor.maesh.dev/v1alpha1",
		kKind:       "IchorInstallation",
		kMetadata:   map[string]any{kName: "ichor"},
		kSpec: map[string]any{
			"version":        "v0.1.0",
			"managementMode": "external",
			kStack:           map[string]any{"promotion": "Manual", "argocd": map[string]any{"values": map[string]any{"a": int64(1)}}},
		},
	}}

	var err error

	for range 50 {
		if err = k8sClient.Create(ctx, obj); err == nil {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("creating the IchorInstallation under the old schema: %v", err)
	}

	if err := unstructured.SetNestedField(obj.Object, map[string]any{"argocd": map[string]any{"publishedTag": "x"}}, "status", kStack); err != nil {
		t.Fatal(err)
	}

	if err := k8sClient.Status().Update(ctx, obj); err != nil {
		t.Fatalf("writing status.stack under the old schema: %v", err)
	}

	live := &unstructured.Unstructured{}
	live.SetAPIVersion("apiextensions.k8s.io/v1")
	live.SetKind(kindCRD)

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: old.GetName()}, live); err != nil {
		t.Fatal(err)
	}

	target := loadCRD(t, "../../config/crd/bases/ichor.maesh.dev_ichorinstallations.yaml")
	if err := checkAdditive(live, target); err != nil {
		t.Fatalf("the installer would refuse the upgrade: %v", err)
	}

	target.SetResourceVersion(live.GetResourceVersion())

	if err := k8sClient.Update(ctx, target); err != nil {
		t.Fatalf("installing the new CRD: %v", err)
	}

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "ichor"}, obj); err != nil {
		t.Fatal(err)
	}

	if err := unstructured.SetNestedField(obj.Object, "v0.2.0", kSpec, "version"); err != nil {
		t.Fatal(err)
	}

	if err := k8sClient.Update(ctx, obj); err != nil {
		t.Fatalf("updating the stored IchorInstallation under the new CRD: %v", err)
	}

	for _, path := range [][]string{{kSpec, "managementMode"}, {kSpec, kStack}, {"status", kStack}} {
		if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, path...); found {
			t.Errorf("%v survived the update: the API server should prune it", path)
		}
	}
}
