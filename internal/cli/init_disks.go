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
	"github.com/siderolabs/talos/pkg/machinery/constants"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// diskRoleOptions bundles init's optional, per-disk-role flags beyond the
// required install disk (architecture.md's "Nodes": each disk gets a
// role -- system, ephemeral, storage, userVolume or unused). All three
// are selectors, not device paths: architecture.md explicitly says disks
// are identified by WWID, serial or by-id path, not /dev/sdX, so these
// take the same CEL expression DiskSelector.Match itself takes (e.g.
// `disk.wwid == "..."` or `disk.dev_path == "/dev/sdb"` if that really is
// the only stable thing a given disk reports) -- init does not translate
// a device path into one on the operator's behalf, since it has no way
// to query the node's disks yet.
type diskRoleOptions struct {
	EphemeralDiskSelector string

	StorageDiskSelector string
	StorageVolumeName   string

	UserVolumeDiskSelector string
	UserVolumeName         string
}

// buildDiskRoleDocuments turns whichever of opts' selectors were given
// into the matching TalosConfigPatchDocument, each a role from
// architecture.md's "Nodes" section: ephemeral (VolumeConfig, the fixed
// system name EPHEMERAL), storage (RawVolumeConfig, an unformatted
// partition for a CSI provisioner) and userVolume (UserVolumeConfig). A
// role with no selector given contributes nothing -- "unused" is also a
// valid role and the default for any disk this command doesn't mention.
//
// None of these set maxSize: that only matters once two roles share one
// disk (architecture.md's shared-disk note on negative maxSize meaning
// "space to leave free", not "space to consume"), and every role here
// gets its own selector, implying its own disk -- the "recommended
// layout" the design names, not the discouraged shared one. Disk
// encryption is not offered either: EncryptionKey.KeyStatic was dropped
// from the mirror type as secret material, and the remaining key kinds
// (KMS, TPM, node ID) are each their own integration this phase does not
// build.
func buildDiskRoleDocuments(opts diskRoleOptions) []ichorv1alpha1.TalosConfigPatchDocument {
	var docs []ichorv1alpha1.TalosConfigPatchDocument

	if opts.EphemeralDiskSelector != "" {
		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{
			Kind: "VolumeConfig",
			VolumeConfig: &typedconfig.VolumeConfigV1Alpha1{
				MetaName: constants.EphemeralPartitionLabel,
				ProvisioningSpec: &typedconfig.ProvisioningSpec{
					DiskSelectorSpec: &typedconfig.DiskSelector{Match: opts.EphemeralDiskSelector},
				},
			},
		})
	}

	if opts.StorageDiskSelector != "" {
		name := opts.StorageVolumeName
		if name == "" {
			name = "storage"
		}

		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{
			Kind: "RawVolumeConfig",
			RawVolumeConfig: &typedconfig.RawVolumeConfigV1Alpha1{
				MetaName: name,
				ProvisioningSpec: &typedconfig.ProvisioningSpec{
					DiskSelectorSpec: &typedconfig.DiskSelector{Match: opts.StorageDiskSelector},
					// "0": no required minimum (Talos rejects a
					// RawVolumeConfig/UserVolumeConfig with neither minSize
					// nor maxSize set); leaving maxSize unset lets it grow
					// to fill its own dedicated disk.
					ProvisioningMinSize: "0",
				},
			},
		})
	}

	if opts.UserVolumeDiskSelector != "" {
		name := opts.UserVolumeName
		if name == "" {
			name = "data"
		}

		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{
			Kind: "UserVolumeConfig",
			UserVolumeConfig: &typedconfig.UserVolumeConfigV1Alpha1{
				MetaName: name,
				ProvisioningSpec: &typedconfig.ProvisioningSpec{
					DiskSelectorSpec:    &typedconfig.DiskSelector{Match: opts.UserVolumeDiskSelector},
					ProvisioningMinSize: "0",
				},
			},
		})
	}

	return docs
}
