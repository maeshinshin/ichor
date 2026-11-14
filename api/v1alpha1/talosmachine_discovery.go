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

package v1alpha1

import (
	"fmt"
	"slices"
	"strings"
)

// TalosMachineDiscoveredDisk is one installable disk the node reported in maintenance mode.
// The identifiers are the ones a TalosMachineDisk selector can name; the size, model and transport tell
// the disks apart for a person. No pattern is set on them: the values are what Talos reported, and a
// status write must not fail on a device name the CRD would not have written.
type TalosMachineDiscoveredDisk struct {
	// wwid is the disk's World Wide Identifier, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	WWID string `json:"wwid,omitempty"`

	// serial is the disk's serial number, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Serial string `json:"serial,omitempty"`

	// byIDPath is the disk's /dev/disk/by-id/ symlink, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ByIDPath string `json:"byIDPath,omitempty"`

	// busPath is the disk's bus path, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	BusPath string `json:"busPath,omitempty"`

	// devPath is the kernel device path, as reported. Not stable across boots.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DevPath string `json:"devPath,omitempty"`

	// sizeBytes is the disk size in bytes.
	// +optional
	SizeBytes int64 `json:"sizeBytes,omitempty"`

	// model is the disk's model name, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Model string `json:"model,omitempty"`

	// transport is the bus the disk is attached over (sata, nvme, scsi, virtio, ...), as reported.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Transport string `json:"transport,omitempty"`
}

// TalosMachineDiscoveredLink is one physical link with a permanent MAC address the node reported in
// maintenance mode. Its PermanentMAC is the value TalosMachineNetwork.MAC matches.
type TalosMachineDiscoveredLink struct {
	// name is the kernel link name (eno1, enp3s0, ...), as reported; it can change between boots.
	// +kubebuilder:validation:MaxLength=256
	// +required
	Name string `json:"name"`

	// permanentMAC is the hardware address burned into the NIC.
	// +kubebuilder:validation:MaxLength=17
	// +required
	PermanentMAC string `json:"permanentMAC"`

	// linkUp is true when the link reports carrier.
	// +optional
	LinkUp bool `json:"linkUp,omitempty"`

	// speedMbit is the negotiated speed in Mbit/s, or 0 when the link does not report one.
	// +optional
	SpeedMbit int32 `json:"speedMbit,omitempty"`

	// driver is the kernel driver that owns the NIC, as reported.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Driver string `json:"driver,omitempty"`
}

// DiskSelectorMatches reports whether the selector names the discovered disk: the selector sets one of
// the identifiers, and it has to equal the same identifier the disk reported.
func DiskSelectorMatches(sel TalosMachineDisk, d TalosMachineDiscoveredDisk) bool {
	return (sel.WWID != "" && sel.WWID == d.WWID) ||
		(sel.Serial != "" && sel.Serial == d.Serial) ||
		(sel.ByIDPath != "" && sel.ByIDPath == d.ByIDPath) ||
		(sel.BusPath != "" && sel.BusPath == d.BusPath) ||
		(sel.DevPath != "" && sel.DevPath == d.DevPath)
}

// describeDiskSelector names a selector the way the CLI's --install-disk syntax does, for a warning.
func describeDiskSelector(sel TalosMachineDisk) string {
	switch {
	case sel.WWID != "":
		return "wwid:" + sel.WWID
	case sel.Serial != "":
		return "serial:" + sel.Serial
	case sel.ByIDPath != "":
		return sel.ByIDPath
	case sel.BusPath != "":
		return sel.BusPath
	default:
		return sel.DevPath
	}
}

// DiscoveryWarnings names what the spec picks that discovery did not see on the node: an
// install disk or disk role that matches none of status.discoveredDisks, or a NIC whose MAC is none of
// status.discoveredLinks. Each list is only checked once it has been recorded, so a machine discovered
// before the lists existed gets no warning. A warning is advice: the disk or NIC may be attached after
// discovery, so nothing is refused for it.
func (m *TalosMachine) DiscoveryWarnings() []string {
	var warns []string

	if len(m.Status.DiscoveredDisks) > 0 {
		check := func(what string, sel TalosMachineDisk) {
			if slices.ContainsFunc(m.Status.DiscoveredDisks, func(d TalosMachineDiscoveredDisk) bool { return DiskSelectorMatches(sel, d) }) {
				return
			}

			warns = append(warns, fmt.Sprintf(
				"%s %q is not one of the %d disks the node reported when it was discovered; check that the disk is attached",
				what, describeDiskSelector(sel), len(m.Status.DiscoveredDisks)))
		}

		if m.Spec.Install != nil {
			check("install disk", m.Spec.Install.Disk)
		}

		for _, r := range m.Spec.Disks {
			check("disk role "+r.Role+" on", r.Disk)
		}
	}

	if n := m.Spec.Network; n != nil && len(m.Status.DiscoveredLinks) > 0 {
		if !slices.ContainsFunc(m.Status.DiscoveredLinks, func(l TalosMachineDiscoveredLink) bool { return strings.EqualFold(l.PermanentMAC, n.MAC) }) {
			warns = append(warns, fmt.Sprintf(
				"network MAC %s is not the permanent address of any of the %d NICs the node reported when it was discovered",
				n.MAC, len(m.Status.DiscoveredLinks)))
		}
	}

	return warns
}
