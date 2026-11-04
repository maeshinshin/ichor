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

package storage

import (
	"fmt"
	"slices"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Verified against Longhorn's official docs (longhorn.io/docs/1.12.1,
// current stable at the time of writing is 1.13.0, API unchanged since
// 1.12.1 for everything used here) and longhorn-manager's own source
// (github.com/longhorn/longhorn-manager, tag v1.12.1,
// k8s/pkg/apis/longhorn/v1beta2/node.go), not guessed.
const (
	// longhornVolumeName is the UserVolumeConfig name Longhorn's Talos
	// Linux support doc's example uses.
	longhornVolumeName = "longhorn"

	// longhornMountPath is where Talos >=1.10's UserVolumeConfig-based
	// setup mounts the disk, per Longhorn's Talos Linux support doc
	// (longhorn.io/docs/1.12.1/advanced-resources/os-distro-specific/
	// talos-linux-support/). Talos <1.10 used /var/lib/longhorn instead;
	// Ichor targets Talos 1.14.3, so only the newer path
	// applies.
	longhornMountPath = "/var/mnt/longhorn"

	// longhornCreateDefaultDiskLabel and longhornDefaultDisksConfigAnnotation
	// are Longhorn's own node registration mechanism (longhorn.io/docs/
	// 1.13.0/nodes-and-volumes/nodes/default-disk-and-node-config/):
	// the label value "config" tells Longhorn's node controller to read
	// the disk list from the annotation below, instead of auto-creating a
	// default disk from whatever Longhorn's own default path setting says.
	longhornCreateDefaultDiskLabel       = "node.longhorn.io/create-default-disk"
	longhornDefaultDisksConfigAnnotation = "node.longhorn.io/default-disks-config"

	// longhornDefaultDisksConfig is the one disk entry longhorn.io/docs/1.13.0's
	// default-disks-config annotation example shows (path, allowScheduling);
	// name and tags are omitted since this preset names and tags nothing.
	longhornDefaultDisksConfig = `[{"path":"` + longhornMountPath + `","allowScheduling":true}]`
)

// longhornV1 is the longhorn-v1 Preset.
type longhornV1 struct{}

func (longhornV1) RequiredExtensions() []string {
	return []string{"siderolabs/iscsi-tools", "siderolabs/util-linux-tools"}
}

func (p longhornV1) CheckExtensions(installed []string) error {
	if missing := slices.DeleteFunc(p.RequiredExtensions(), func(e string) bool {
		return slices.Contains(installed, e)
	}); len(missing) > 0 {
		return fmt.Errorf("storage: longhorn-v1 needs system extensions not in the node's installer image: %s",
			strings.Join(missing, ", "))
	}

	return nil
}

// TalosPatches implements Preset. It returns a UserVolumeConfig document
// provisioning the disk diskSelector matches. The kubelet bind mount
// Longhorn's Talos Linux support doc shows alongside it is KubeletMounts, not
// a document here: how (and whether) it can be expressed depends on the
// version contract.
func (longhornV1) TalosPatches(diskSelector string) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if diskSelector == "" {
		return nil, fmt.Errorf("storage: diskSelector is required")
	}

	return []ichorv1alpha1.TalosConfigPatchDocument{
		{
			Kind: "UserVolumeConfig",
			UserVolumeConfig: &typedconfig.UserVolumeConfigV1Alpha1{
				MetaName: longhornVolumeName,
				ProvisioningSpec: &typedconfig.ProvisioningSpec{
					DiskSelectorSpec: &typedconfig.DiskSelector{Match: diskSelector},
					// Talos rejects a UserVolumeConfig with neither minSize nor maxSize;
					// Longhorn is handed the whole disk.
					ProvisioningMaxSize: "100%",
				},
			},
		},
	}, nil
}

// KubeletMounts implements Preset: the bind mount Longhorn's Talos Linux support doc shows, so
// the volume UserVolumeConfig provisions is visible to the kubelet.
func (longhornV1) KubeletMounts() []KubeletMount {
	return []KubeletMount{{
		Destination: longhornMountPath, Source: longhornMountPath, Type: "bind",
		Options: []string{"bind", "rshared", "rw"},
	}}
}

func (longhornV1) NodeLabels() map[string]string {
	return map[string]string{longhornCreateDefaultDiskLabel: "config"}
}

func (longhornV1) NodeAnnotations() map[string]string {
	return map[string]string{longhornDefaultDisksConfigAnnotation: longhornDefaultDisksConfig}
}

func (longhornV1) CheckConsistency(live LiveConfig) error {
	if !live.CreateDefaultDiskOnLabeledNodes {
		return fmt.Errorf("storage: longhorn-v1 registers disks through node labels and annotations, " +
			`but Longhorn's "Create Default Disk on Labeled Nodes" setting is disabled`)
	}

	return nil
}

// BeginEvacuation implements EvacuationHook. Verified against
// longhorn-manager v1.9.1's own types and admission webhook:
// NodeSpec and DiskSpec both carry allowScheduling and evictionRequested,
// and the webhook rejects evictionRequested=true while allowScheduling is
// still true -- so these two values must land as two separate writes, in
// this order, never one combined patch.
func (longhornV1) BeginEvacuation() (allowScheduling, evictionRequested bool) {
	return false, true
}

func (longhornV1) EndEvacuation() (allowScheduling, evictionRequested bool) {
	return true, false
}
