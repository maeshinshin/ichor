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

import ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"

// rookV1 is the rook-v1 Preset. Rook's OSDs take raw devices or raw partitions with no
// filesystem (Rook v1.19.6, Documentation/Getting-Started/Prerequisites/prerequisites.md, "Ceph
// Prerequisites"), and Talos's UserVolumeConfig formats what it provisions, so the preset writes
// no volume document. Rook takes its devices from its own CephCluster settings rather than from a
// node label or annotation Ichor sets, and Ichor writes nothing Rook could contradict.
//
// shortcut: writes no disk config, so Ichor does not prepare the disk Rook is given, and a
// formatting document from elsewhere on the node is not refused; drop this once the Talos-side
// decision says whether to write a RawVolumeConfig (an unformatted partition, Talos 1.14).
type rookV1 struct{}

// RequiredExtensions implements Preset. Rook's prerequisites name no Talos system extension. They
// do require the rbd kernel module; whether the Talos kernel provides it is not decided here.
func (rookV1) RequiredExtensions() []string {
	return nil
}

// CheckExtensions implements Preset. There is no extension to find missing.
func (rookV1) CheckExtensions([]string) error {
	return nil
}

// TalosPatches implements Preset. It returns no documents for any selector: the selector only
// chooses a disk for the storage system to take, and rook-v1 hands Rook no disk.
func (rookV1) TalosPatches(string) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	return nil, nil
}

// KubeletMounts implements Preset. Rook's host paths (dataDirHostPath, default /var/lib/rook) are
// reached from its own pods, not through a kubelet extra mount.
func (rookV1) KubeletMounts() []KubeletMount {
	return nil
}

// NodeLabels implements Preset. Rook selects nodes through its CephCluster, not through a node label
// Ichor sets.
func (rookV1) NodeLabels() map[string]string {
	return nil
}

// NodeAnnotations implements Preset. Rook registers no disk through a node annotation.
func (rookV1) NodeAnnotations() map[string]string {
	return nil
}

// CheckConsistency implements Preset. Ichor hands Rook nothing that its configuration could
// contradict, and Ichor does not read Rook's resources, so there is no mismatch to report.
func (rookV1) CheckConsistency(LiveConfig) error {
	return nil
}
