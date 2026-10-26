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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
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
