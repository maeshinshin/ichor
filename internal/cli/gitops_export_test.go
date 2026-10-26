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
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// exportTestCluster is the fixture cluster name shared by every test in
// this file.
const exportTestCluster = "export-cluster"

func exportTestOptions(dir string) gitopsExportOptions {
	return gitopsExportOptions{
		Cluster:         exportTestCluster,
		Namespace:       testNamespace,
		Dir:             dir,
		ArgoCDNamespace: "argocd",
	}
}

// TestRunGitopsExport_WritesApprovedCRsOnly proves export's allow-list:
// the TalosCluster, an approved TalosMachine, a TalosConfigPatch and a
// TalosStorage belonging to the cluster all land under clusters/<cluster>/,
// while an unapproved TalosMachine (not yet an operator decision) and a
// Secret (never collected in the first place, so never a candidate) do
// not. A TalosOperation cannot be constructed here -- that CRD does not
// exist yet (it is another agent's concurrent work) -- but export's
// allow-list design means a new CRD kind is excluded by never being
// listed, not by a deny-list that would need updating once it lands.
func TestRunGitopsExport_WritesApprovedCRsOnly(t *testing.T) {
	cluster := newTestCluster()
	cluster.Name = exportTestCluster

	approved := newTestMachine(exportTestCluster)
	approved.Name = "approved-machine"

	unapproved := newTestMachine(exportTestCluster)
	unapproved.Name = "unapproved-machine"
	unapproved.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending

	patch := sysctlPatch(exportTestCluster, map[string]string{testSysctlKey: "1"})

	storage := &ichorv1alpha1.TalosStorage{
		Name: "my-storage", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosStorageSpec{ClusterRef: corev1.LocalObjectReference{Name: exportTestCluster}},
	}

	backup := &ichorv1alpha1.TalosEtcdBackup{
		Name: "my-backup", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{ClusterRef: corev1.LocalObjectReference{Name: exportTestCluster}},
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(cluster, approved, unapproved, &patch, storage, backup).Build()

	dir := t.TempDir()

	var warn bytes.Buffer
	if err := runGitopsExport(context.Background(), exportTestOptions(dir), k8sClient, &warn); err != nil {
		t.Fatalf("runGitopsExport: %v", err)
	}

	clusterDir := filepath.Join(dir, "clusters", exportTestCluster)

	mustExist := []string{
		"talos-cluster.yaml",
		"talos-machine-approved-machine.yaml",
		"talos-config-patch-fixture-sysctl.yaml",
		"talos-storage-my-storage.yaml",
		"talos-etcd-backup-my-backup.yaml",
	}

	for _, name := range mustExist {
		if _, err := os.Stat(filepath.Join(clusterDir, name)); err != nil {
			t.Fatalf("expected %s to exist: %v", name, err)
		}
	}

	if _, err := os.Stat(filepath.Join(clusterDir, "talos-machine-unapproved-machine.yaml")); err == nil {
		t.Fatal("expected the unapproved machine NOT to be exported, but it was")
	}

	entries, err := os.ReadDir(clusterDir)
	if err != nil {
		t.Fatalf("reading %s: %v", clusterDir, err)
	}

	if len(entries) != len(mustExist) {
		t.Fatalf("clusters/%s/ has %d files, want exactly %d (%v)", exportTestCluster, len(entries), len(mustExist), entries)
	}
}

// TestRunGitopsExport_WarnsWhenUnapprovedMachineIsFluxTracked proves the
// "warn, do not export" path for a TalosMachine that is unapproved but
// already carries a managedFields entry for Flux's own field manager
// (gitopstracking.IsTracked, the cheap single-object signal
// architecture.md names for Flux, since Flux marks nothing else on the
// object itself).
func TestRunGitopsExport_WarnsWhenUnapprovedMachineIsFluxTracked(t *testing.T) {
	cluster := newTestCluster()
	cluster.Name = exportTestCluster

	machine := newTestMachine(exportTestCluster)
	machine.Name = "flux-owned-machine"
	machine.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStatePending
	machine.ManagedFields = []metav1.ManagedFieldsEntry{{
		Manager: gitopstracking.FluxKustomizeControllerFieldManager, Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: testAPIVersion, FieldsType: "FieldsV1",
	}}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, machine).Build()

	dir := t.TempDir()

	var warn bytes.Buffer
	if err := runGitopsExport(context.Background(), exportTestOptions(dir), k8sClient, &warn); err != nil {
		t.Fatalf("runGitopsExport: %v", err)
	}

	if !strings.Contains(warn.String(), "Flux") {
		t.Fatalf("expected a warning naming Flux, got: %s", warn.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "clusters", exportTestCluster, "talos-machine-flux-owned-machine.yaml")); err == nil {
		t.Fatal("expected the Flux-tracked, unapproved machine NOT to be exported, but it was")
	}
}

// TestRunGitopsExport_ClusterNotFound proves a missing TalosCluster fails
// loudly rather than silently writing an empty skeleton.
func TestRunGitopsExport_ClusterNotFound(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	dir := t.TempDir()

	var warn bytes.Buffer
	if err := runGitopsExport(context.Background(), exportTestOptions(dir), k8sClient, &warn); err == nil {
		t.Fatal("expected an error for a missing TalosCluster, got nil")
	}
}

// declaredMetadata is every metadata key export may write.
var declaredMetadata = strings.Fields("name namespace labels annotations")

// serverStamp fills in everything the API server (or another controller)
// stamps on a live object, which a fresh fake-client fixture lacks and
// which must never reach Git.
func serverStamp(o metav1.Object) {
	o.SetUID("11111111-2222-3333-4444-555555555555")
	o.SetResourceVersion("424242")
	o.SetGeneration(7)
	o.SetCreationTimestamp(metav1.Now())
	o.SetManagedFields([]metav1.ManagedFieldsEntry{{
		Manager: "kubectl", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: testAPIVersion, FieldsType: "FieldsV1",
	}})
	o.SetFinalizers([]string{"ichor.maesh.dev/machine-removal"})
	o.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "owner", UID: "u"}})
	o.SetAnnotations(map[string]string{
		"kubectl.kubernetes.io/last-applied-configuration": "{}",
		gitopstracking.ArgoTrackingAnnotation:              "argocd:Application:argocd/old",
		deleteguard.AllowDeleteAnnotation:                  deleteguard.AllowDeleteValue,
	})
	o.SetLabels(map[string]string{"team": "platform"})
}

// TestRunGitopsExport_StripsServerAndRuntimeState proves export writes what
// an operator declared, not what the cluster looks like right now: no
// status, no server-assigned metadata, and no annotation another tool
// stamped on -- but the operator's own labels and annotations (the
// allow-delete opt-in here) survive. resourceVersion in Git would make
// Argo CD's apply conflict; uid would make the files unusable on a
// different cluster.
func TestRunGitopsExport_StripsServerAndRuntimeState(t *testing.T) {
	cluster := newTestCluster()
	cluster.Name = exportTestCluster
	cluster.Status = ichorv1alpha1.TalosClusterStatus{ObservedGeneration: 3}

	machine := newTestMachine(exportTestCluster)
	patch := sysctlPatch(exportTestCluster, map[string]string{testSysctlKey: "1"})
	storage := &ichorv1alpha1.TalosStorage{
		Name: "my-storage", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosStorageSpec{ClusterRef: corev1.LocalObjectReference{Name: exportTestCluster}},
	}
	backup := &ichorv1alpha1.TalosEtcdBackup{
		Name: "my-backup", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosEtcdBackupSpec{ClusterRef: corev1.LocalObjectReference{Name: exportTestCluster}},
	}

	objs := []client.Object{cluster, machine, &patch, storage, backup}
	for _, o := range objs {
		serverStamp(o)
	}

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()

	dir := t.TempDir()

	var warn bytes.Buffer
	if err := runGitopsExport(context.Background(), exportTestOptions(dir), k8sClient, &warn); err != nil {
		t.Fatalf("runGitopsExport: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "clusters", exportTestCluster, "*.yaml"))
	if err != nil || len(files) != 5 {
		t.Fatalf("expected 5 exported CRs, got %v (err %v)", files, err)
	}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}

		if doc["apiVersion"] == nil || doc["kind"] == nil {
			t.Errorf("%s: missing apiVersion/kind:\n%s", f, raw)
		}

		if _, ok := doc["status"]; ok {
			t.Errorf("%s: status was exported:\n%s", f, raw)
		}

		meta, _ := doc["metadata"].(map[string]any)
		for key := range meta {
			if !slices.Contains(declaredMetadata, key) {
				t.Errorf("%s: metadata.%s was exported:\n%s", f, key, raw)
			}
		}

		ann, _ := meta["annotations"].(map[string]any)
		if ann[deleteguard.AllowDeleteAnnotation] != deleteguard.AllowDeleteValue {
			t.Errorf("%s: the operator's allow-delete annotation was dropped:\n%s", f, raw)
		}

		if len(ann) != 1 {
			t.Errorf("%s: annotations = %v, want only allow-delete", f, ann)
		}

		if lbl, _ := meta["labels"].(map[string]any); lbl["team"] != "platform" {
			t.Errorf("%s: the operator's label was dropped:\n%s", f, raw)
		}
	}
}
