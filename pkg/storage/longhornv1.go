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
	"encoding/json"
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
	// Ichor targets Talos 1.14.3 (ADR 0003), so only the newer path
	// applies.
	longhornMountPath = "/var/mnt/longhorn"

	// longhornCreateDefaultDiskLabel and longhornDefaultDisksConfigAnnotation
	// are Longhorn's own node registration mechanism (longhorn.io/docs/
	// 1.13.0/nodes-and-volumes/nodes/default-disk-and-node-config/):
	// the label value "config" tells Longhorn's node controller to read
	// the disk list from the annotation below, instead of auto-creating a
	// default disk from whatever Longhorn's own default path setting says.
	longhornCreateDefaultDiskLabel            = "node.longhorn.io/create-default-disk"
	longhornDefaultDisksConfigAnnotation      = "node.longhorn.io/default-disks-config"
	longhornCreateDefaultDiskLabelValueConfig = "config"
)

// longhornDiskConfig mirrors the one disk entry longhorn.io/docs/1.13.0's
// default-disks-config annotation example shows (path, allowScheduling);
// name and tags are omitted since this preset names and tags nothing.
type longhornDiskConfig struct {
	Path            string `json:"path"`
	AllowScheduling bool   `json:"allowScheduling"`
}

// longhornV1 is the longhorn-v1 Preset.
type longhornV1 struct{}

// RequiredExtensions implements Preset.
func (longhornV1) RequiredExtensions() []string {
	return []string{"siderolabs/iscsi-tools", "siderolabs/util-linux-tools"}
}

// CheckExtensions implements Preset.
func (p longhornV1) CheckExtensions(installed []string) error {
	var missing []string

	for _, want := range p.RequiredExtensions() {
		if !slices.Contains(installed, want) {
			missing = append(missing, want)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("storage: longhorn-v1 needs system extensions not in the node's installer image: %s",
			strings.Join(missing, ", "))
	}

	return nil
}

// TalosPatches implements Preset. It returns a UserVolumeConfig document
// provisioning the disk diskSelector matches, and a Config document adding
// the kubelet bind mount Longhorn's Talos Linux support doc's example
// shows alongside it -- without the mount, the volume UserVolumeConfig
// provisions is invisible to the kubelet, and so to Longhorn.
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
				},
			},
		},
		{
			Kind: "Config",
			Config: &typedconfig.Config{
				MachineConfig: &typedconfig.MachineConfig{
					MachineKubelet: &typedconfig.KubeletConfig{
						KubeletExtraMounts: []*typedconfig.ExtraMount{
							{
								Destination: longhornMountPath,
								Type:        "bind",
								Source:      longhornMountPath,
								Options:     []string{"bind", "rshared", "rw"},
							},
						},
					},
				},
			},
		},
	}, nil
}

// NodeLabels implements Preset.
func (longhornV1) NodeLabels() map[string]string {
	return map[string]string{longhornCreateDefaultDiskLabel: longhornCreateDefaultDiskLabelValueConfig}
}

// NodeAnnotations implements Preset.
func (longhornV1) NodeAnnotations() map[string]string {
	// Marshaling a fixed literal value never fails; the error is checked
	// anyway rather than ignored, per the project's own rule against
	// silently dropped errors.
	config, err := json.Marshal([]longhornDiskConfig{{Path: longhornMountPath, AllowScheduling: true}})
	if err != nil {
		panic(fmt.Errorf("storage: marshaling longhorn-v1's default-disks-config: %w", err))
	}

	return map[string]string{longhornDefaultDisksConfigAnnotation: string(config)}
}

// CheckConsistency implements Preset.
func (longhornV1) CheckConsistency(live LiveConfig) error {
	if !live.CreateDefaultDiskOnLabeledNodes {
		return fmt.Errorf("storage: longhorn-v1 registers disks through node labels and annotations, " +
			`but Longhorn's "Create Default Disk on Labeled Nodes" setting is disabled`)
	}

	return nil
}
