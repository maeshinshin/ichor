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
	"fmt"
	"os"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// installOldCRD installs the CRD and removes it again when the test ends, waiting until it is gone,
// so the test can run more than once against the same API server (-count=N).
func installOldCRD(t *testing.T, old *unstructured.Unstructured) {
	t.Helper()

	ctx := context.Background()

	if err := k8sClient.Create(ctx, old); err != nil {
		t.Fatalf("installing the old CRD: %v", err)
	}

	t.Cleanup(func() {
		crd := &unstructured.Unstructured{}
		crd.SetAPIVersion("apiextensions.k8s.io/v1")
		crd.SetKind(kindCRD)
		crd.SetName(old.GetName())

		if err := k8sClient.Delete(ctx, crd); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("deleting the CRD: %v", err)

			return
		}

		for range 300 {
			err := k8sClient.Get(ctx, types.NamespacedName{Name: old.GetName()}, crd)
			if apierrors.IsNotFound(err) {
				return
			}

			time.Sleep(100 * time.Millisecond)
		}

		t.Errorf("the CRD %s was not removed", old.GetName())
	})
}

// createWhenEstablished creates obj, retrying while the freshly installed CRD is still being
// established asynchronously.
func createWhenEstablished(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()

	var err error

	for range 50 {
		if err = k8sClient.Create(context.Background(), obj); err == nil {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("creating the %s under the old schema: %v", obj.GetKind(), err)
}

// upgradeCRD replaces the installed old CRD with the one at targetPath, after checkAdditive
// accepts the change.
func upgradeCRD(t *testing.T, old *unstructured.Unstructured, targetPath string) {
	t.Helper()

	ctx := context.Background()

	live := &unstructured.Unstructured{}
	live.SetAPIVersion("apiextensions.k8s.io/v1")
	live.SetKind(kindCRD)

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: old.GetName()}, live); err != nil {
		t.Fatal(err)
	}

	target := loadCRD(t, targetPath)
	if err := checkAdditive(live, target); err != nil {
		t.Fatalf("the installer would refuse the upgrade: %v", err)
	}

	target.SetResourceVersion(live.GetResourceVersion())

	if err := k8sClient.Update(ctx, target); err != nil {
		t.Fatalf("installing the new CRD: %v", err)
	}
}

// A TalosCluster stored under the schema that still had spec.gitops (and spec.cni.handover) must
// stay writable once the current CRD, which no longer has them, is installed (ADR 0101): the
// installer's CRD check allows the removal, the API server prunes the retired fields and no
// validation rejects the object for carrying them.
func TestStoredTalosClusterWithRetiredFieldsSurvivesTheCRDUpgrade(t *testing.T) {
	// testdata is the TalosCluster CRD as it was before the removal (git show c7902de:config/crd/bases/...).
	old := loadCRD(t, "testdata/talosclusters-before-adr0101.yaml")
	installOldCRD(t, old)

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

	createWhenEstablished(t, obj)

	if _, found, _ := unstructured.NestedMap(obj.Object, kSpec, kGitops); !found {
		t.Fatal("the old schema did not keep spec.gitops")
	}

	upgradeCRD(t, old, "../../config/crd/bases/ichor.maesh.dev_talosclusters.yaml")

	updateUntilPruned(t, types.NamespacedName{Namespace: "default", Name: "old"}, obj,
		[]string{kSpec, "endpoint", "address"}, "10.0.0.2",
		[][]string{{kSpec, kGitops}, {kSpec, "cni", kHandover}})
}

// updateUntilPruned updates the stored object (setting path to a value that differs on every try)
// until the response no longer carries any of the retired paths. The API server swaps in the new
// CRD schema asynchronously after the CRD update returns, so an early update is still pruned by
// the old schema; pruning applies to the whole object on every update, so a later try prunes what
// an earlier one left. The paths must be gone before the deadline, or the test fails.
func updateUntilPruned(t *testing.T, key types.NamespacedName, obj *unstructured.Unstructured, path []string, value string, retired [][]string) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)

	var left [][]string

	for try := 0; ; try++ {
		if err := k8sClient.Get(ctx, key, obj); err != nil {
			t.Fatal(err)
		}

		if err := unstructured.SetNestedField(obj.Object, fmt.Sprintf("%s-%d", value, try), path...); err != nil {
			t.Fatal(err)
		}

		if err := k8sClient.Update(ctx, obj); err != nil {
			t.Fatalf("updating the stored object under the new CRD: %v", err)
		}

		left = nil

		for _, p := range retired {
			if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, p...); found {
				left = append(left, p)
			}
		}

		if len(left) == 0 {
			return
		}

		if time.Now().After(deadline) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	for _, p := range left {
		t.Errorf("%v survived the update: the API server should prune it", p)
	}
}

// The same for IchorInstallation: spec.managementMode, spec.stack and status.stack are retired.
func TestStoredIchorInstallationWithRetiredFieldsSurvivesTheCRDUpgrade(t *testing.T) {
	ctx := context.Background()

	old := loadCRD(t, "testdata/ichorinstallations-before-adr0101.yaml")
	installOldCRD(t, old)

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

	createWhenEstablished(t, obj)

	if err := unstructured.SetNestedField(obj.Object, map[string]any{"argocd": map[string]any{"publishedTag": "x"}}, "status", kStack); err != nil {
		t.Fatal(err)
	}

	if err := k8sClient.Status().Update(ctx, obj); err != nil {
		t.Fatalf("writing status.stack under the old schema: %v", err)
	}

	upgradeCRD(t, old, "../../config/crd/bases/ichor.maesh.dev_ichorinstallations.yaml")

	updateUntilPruned(t, types.NamespacedName{Name: "ichor"}, obj,
		[]string{kSpec, "version"}, "v0.2.0",
		[][]string{{kSpec, "managementMode"}, {kSpec, kStack}, {"status", kStack}})
}
