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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	storageDiskSelector = `disk.transport == "nvme"`
	storageRoleLabel    = "storage"
	longhornDiskLabel   = "node.longhorn.io/create-default-disk"
	storageSelectorKey  = "role"
	kindUserVolume      = "UserVolumeConfig"
)

func testStorage(name string, sel map[string]string) ichorv1alpha1.TalosStorage {
	return ichorv1alpha1.TalosStorage{
		Namespace: testNamespace, Name: name,
		Spec: ichorv1alpha1.TalosStorageSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: testClusterName},
			Nodes:        metav1.LabelSelector{MatchLabels: sel},
			DiskPreset:   "longhorn-v1",
			DiskSelector: storageDiskSelector,
		},
	}
}

// TestRenderStorage: layer 1 lands in the config of a machine the TalosStorage selects, and in
// no other machine's.
func TestRenderStorage(t *testing.T) {
	cluster := testCluster("true")
	bundle := testBundle(t)
	sys := &SystemInputs{Storage: []ichorv1alpha1.TalosStorage{
		testStorage("longhorn", map[string]string{storageSelectorKey: storageRoleLabel}),
	}}
	selected := testMachine("node-a", roleWorker, map[string]string{storageSelectorKey: storageRoleLabel})
	other := testMachine("node-b", roleWorker, map[string]string{storageSelectorKey: "compute"})

	out, _, err := Render(cluster, selected, bundle, nil, sys)
	if err != nil {
		t.Fatalf("Render(selected): %v", err)
	}

	requireAll(t, string(out), kindUserVolume, "name: longhorn", storageDiskSelector,
		"/var/mnt/longhorn", longhornDiskLabel, "node.longhorn.io/default-disks-config")

	out, _, err = Render(cluster, other, bundle, nil, sys)
	if err != nil {
		t.Fatalf("Render(other): %v", err)
	}

	requireNone(t, string(out), kindUserVolume, "longhorn")

	t.Run("another cluster's TalosStorage never applies", func(t *testing.T) {
		foreign := testStorage("foreign", map[string]string{storageSelectorKey: storageRoleLabel})
		foreign.Spec.ClusterRef.Name = "someone-else"

		out, _, err := Render(cluster, selected, bundle, nil, &SystemInputs{Storage: []ichorv1alpha1.TalosStorage{foreign}})
		if err != nil {
			t.Fatal(err)
		}

		requireNone(t, string(out), kindUserVolume)
	})

	t.Run("a patch overrides the system layer", func(t *testing.T) {
		patch := ichorv1alpha1.TalosConfigPatch{
			Namespace: testNamespace, Name: "override-size",
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
				Layer:      layerMachine,
				Target:     &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: "node-a"}},
				Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
					Kind: kindUserVolume,
					UserVolumeConfig: &typedconfig.UserVolumeConfigV1Alpha1{
						MetaName: "longhorn",
						ProvisioningSpec: &typedconfig.ProvisioningSpec{
							DiskSelectorSpec: &typedconfig.DiskSelector{Match: storageDiskSelector}, ProvisioningMaxSize: "50%",
						},
					},
				}},
			},
		}

		out, _, err := Render(cluster, selected, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, sys)
		if err != nil {
			t.Fatal(err)
		}

		requireAll(t, string(out), "maxSize: 50%")
		requireNone(t, string(out), "maxSize: 100%")
	})

	// Under a pre-1.14 contract the base config uses the legacy machine.* blocks, so layer 1
	// renders its labels, annotations and the kubelet mount there; under 1.14+ the standalone
	// documents carry no kubelet mount field, so the mount is absent by design.
	t.Run("pre-1.14 contract renders the legacy kubelet mount", func(t *testing.T) {
		legacy := testCluster("true")
		legacy.Spec.VersionContract = contractV112

		out, _, err := Render(legacy, selected, bundle, nil, sys)
		if err != nil {
			t.Fatalf("Render(v1.12): %v", err)
		}

		requireAll(t, string(out), "/var/mnt/longhorn", "rshared", longhornDiskLabel)
	})
}
