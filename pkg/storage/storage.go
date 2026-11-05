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

// Package storage defines what a storage-system disk preset is
// (docs/design/architecture.md, "Storage"). Ichor owns layer 1 (Talos-side
// preparation) and layer 4 (node-operation coordination: evacuate before
// drain). Preset is layer 1: given a preset name and a disk selector, what
// Talos-side config does the storage system need, and does the node's
// installer image already have what that preset requires. EvacuationHook
// (evacuation.go) is layer 4's storage-system side: what to write to evacuate
// a node or disk, and whether it is safe to proceed to drain. Installing or
// operating the storage system is never Ichor's job. This package
// makes no cluster call: it returns data and decisions for a caller
// (internal/controller) to act on.
package storage

import (
	"fmt"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// LonghornV1 is TalosStorage.spec.diskPreset's first and, for now, only
// allowed value.
const LonghornV1 = "longhorn-v1"

// LiveConfig is what Preset.CheckConsistency compares against: the storage
// system's own live configuration, as read by the caller (the future
// reconciler; reading it is not this package's job).
type LiveConfig struct {
	// CreateDefaultDiskOnLabeledNodes reports whether the storage system is
	// configured to pick up the disks Ichor registers through node labels
	// and annotations (architecture.md: "a consistency check confirms the
	// storage system is configured to honour that").
	CreateDefaultDiskOnLabeledNodes bool
}

// KubeletMount is one kubelet extra mount (machine.kubelet.extraMounts' shape).
type KubeletMount struct {
	Destination string
	Source      string
	Type        string
	Options     []string
}

// Preset is a storage system's layer-1 shape: the system extensions its
// disk handling needs, the Talos-side config documents that provision and
// mount the disk, the node label and annotation that register the
// mounted disk with the storage system (architecture.md: "disks are
// registered ... through its own node label and annotation mechanism, not
// by writing its CRs"), and a consistency check against the storage
// system's own live configuration. A new preset is a new implementation of
// this interface and nothing else.
type Preset interface {
	// RequiredExtensions lists the Talos system extensions this preset's
	// disk handling needs. Missing ones block the preset rather than being
	// silently worked around: adding one is an installer-image change,
	// architecture.md says, and "goes through the same rolling procedure as
	// a Talos upgrade" -- not something to paper over here.
	RequiredExtensions() []string

	// CheckExtensions returns an error naming whichever of
	// RequiredExtensions is missing from installed (the node's installer
	// image's extension list, as the caller reads it -- this package makes
	// no call to Image Factory or anywhere else to find out).
	CheckExtensions(installed []string) error

	// TalosPatches returns the Talos-side config documents (volumes, kernel
	// modules, sysctls) this preset needs for a disk matching diskSelector
	// (a Talos disk-selector CEL expression, the same language
	// provisioning.diskSelector.match uses), meant to be applied as a
	// TalosConfigPatch. It returns an error if diskSelector is empty.
	TalosPatches(diskSelector string) ([]ichorv1alpha1.TalosConfigPatchDocument, error)

	// KubeletMounts returns the bind mounts the kubelet needs to see the volumes
	// TalosPatches provisions. Kept apart from TalosPatches because Talos expresses
	// kubelet extra mounts only in the legacy machine.kubelet block: a cluster whose
	// version contract uses the standalone KubeletConfig document (Talos 1.14+) has no
	// field for them, and pkg/render decides per contract whether and how to apply these.
	KubeletMounts() []KubeletMount

	// NodeLabels returns the Kubernetes Node labels that register a node
	// with the storage system through its own label mechanism.
	NodeLabels() map[string]string

	// NodeAnnotations returns the Kubernetes Node annotations that
	// register the mounted disk with the storage system through its own
	// annotation mechanism.
	NodeAnnotations() map[string]string

	// CheckConsistency compares live against what this preset requires and
	// returns a non-nil error describing any mismatch, reported by the
	// caller as a health failure. It makes no cluster calls itself: live is
	// already read.
	CheckConsistency(live LiveConfig) error
}

// NewPreset returns the Preset implementation for name, the same value
// TalosStorage.spec.diskPreset's enum allows, or an error for any other
// name.
func NewPreset(name string) (Preset, error) {
	switch name {
	case LonghornV1:
		return longhornV1{}, nil
	default:
		return nil, fmt.Errorf("storage: unknown disk preset %q", name)
	}
}
