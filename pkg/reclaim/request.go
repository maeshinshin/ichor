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

package reclaim

import (
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ResetRequest is the one definition of what an Ichor reset sends:
// STATE and EPHEMERAL named by label, a reboot back to maintenance mode, and
// nothing else. It never uses Mode USER_DISKS (the server rejects that next to
// a non-empty SystemPartitionsToWipe) and never leaves the partition list
// empty (which would wipe the whole system disk, boot and EFI included).
//
// graceful asks Talos to cordon, drain and leave etcd first; it is for a node
// whose cluster is still reachable. The controller's removal passes false: it
// has already drained and left etcd itself.
func ResetRequest(graceful bool) *machineapi.ResetRequest {
	return &machineapi.ResetRequest{
		Graceful: graceful,
		Reboot:   true,
		SystemPartitionsToWipe: []*machineapi.ResetPartitionSpec{
			{Label: constants.StatePartitionLabel, Wipe: true},
			{Label: constants.EphemeralPartitionLabel, Wipe: true},
		},
	}
}

// ResetRequestWipe is ResetRequest with an optional wipe mode (TalosOperation.spec.node.wipeMode).
// "system-disk" and "all" name no partitions, which is what makes Talos erase the whole system disk
// (boot and EFI included, so the node boots a boot image again instead of its installed UKI). Neither
// wipes user disks: Talos wipes only the user disks a request enumerates, and these send none. Empty is ResetRequest.
func ResetRequestWipe(graceful bool, mode string) *machineapi.ResetRequest {
	switch mode {
	case ichorv1alpha1.WipeModeSystemDisk:
		return &machineapi.ResetRequest{Graceful: graceful, Reboot: true, Mode: machineapi.ResetRequest_SYSTEM_DISK}
	case ichorv1alpha1.WipeModeAll:
		return &machineapi.ResetRequest{Graceful: graceful, Reboot: true, Mode: machineapi.ResetRequest_ALL}
	}

	return ResetRequest(graceful)
}
