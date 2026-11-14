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
	"strings"
	"testing"
)

const (
	devVdz    = "/dev/vdz"
	serialSDA = "S-SDA"
	nicName   = "eno1"
)

// TestDiscoveryWarnings is the check on an approval: the install disk, the disk roles and the
// NIC are compared with what discovery recorded, and nothing is judged until something was recorded.
func TestDiscoveryWarnings(t *testing.T) {
	const (
		nic      = "aa:bb:cc:dd:ee:01"
		otherMAC = "aa:bb:cc:dd:ee:02"
	)

	sda := TalosMachineDiscoveredDisk{Serial: serialSDA, ByIDPath: "/dev/disk/by-id/ata-sda", DevPath: "/dev/sda"}
	nvme := TalosMachineDiscoveredDisk{WWID: "nvme.1", DevPath: "/dev/nvme0n1"}

	cases := []struct {
		name string
		m    TalosMachine
		want []string // substrings, one per expected warning, in order
	}{
		{
			name: "nothing recorded, nothing judged",
			m: TalosMachine{Spec: TalosMachineSpec{
				Install: &TalosMachineInstall{Disk: TalosMachineDisk{DevPath: devVdz}},
				Network: &TalosMachineNetwork{MAC: otherMAC},
			}},
		},
		{
			name: "install disk picked from the list",
			m: TalosMachine{
				Spec:   TalosMachineSpec{Install: &TalosMachineInstall{Disk: TalosMachineDisk{Serial: serialSDA}}},
				Status: TalosMachineStatus{DiscoveredDisks: []TalosMachineDiscoveredDisk{sda, nvme}},
			},
		},
		{
			name: "install disk not discovered",
			m: TalosMachine{
				Spec:   TalosMachineSpec{Install: &TalosMachineInstall{Disk: TalosMachineDisk{DevPath: devVdz}}},
				Status: TalosMachineStatus{DiscoveredDisks: []TalosMachineDiscoveredDisk{sda, nvme}},
			},
			want: []string{"install disk \"/dev/vdz\" is not one of the 2 disks"},
		},
		{
			name: "a disk role that is not discovered is named by its role",
			m: TalosMachine{
				Spec: TalosMachineSpec{
					Install: &TalosMachineInstall{Disk: TalosMachineDisk{Serial: serialSDA}},
					Disks: []TalosMachineDiskRole{{
						Role: MachineDiskRoleUserVolume, Name: "data", Disk: TalosMachineDisk{WWID: "missing"},
					}},
				},
				Status: TalosMachineStatus{DiscoveredDisks: []TalosMachineDiscoveredDisk{sda, nvme}},
			},
			want: []string{"disk role userVolume on \"wwid:missing\""},
		},
		{
			name: "a NIC matched case-insensitively",
			m: TalosMachine{
				Spec: TalosMachineSpec{Network: &TalosMachineNetwork{MAC: strings.ToUpper(nic)}},
				Status: TalosMachineStatus{DiscoveredLinks: []TalosMachineDiscoveredLink{
					{Name: nicName, PermanentMAC: nic},
				}},
			},
		},
		{
			name: "a NIC that was not reported",
			m: TalosMachine{
				Spec: TalosMachineSpec{Network: &TalosMachineNetwork{MAC: otherMAC}},
				Status: TalosMachineStatus{DiscoveredLinks: []TalosMachineDiscoveredLink{
					{Name: nicName, PermanentMAC: nic},
				}},
			},
			want: []string{"network MAC " + otherMAC + " is not the permanent address"},
		},
		{
			name: "both wrong, both named in order",
			m: TalosMachine{
				Spec: TalosMachineSpec{
					Install: &TalosMachineInstall{Disk: TalosMachineDisk{DevPath: devVdz}},
					Network: &TalosMachineNetwork{MAC: otherMAC},
				},
				Status: TalosMachineStatus{
					DiscoveredDisks: []TalosMachineDiscoveredDisk{sda},
					DiscoveredLinks: []TalosMachineDiscoveredLink{{Name: nicName, PermanentMAC: nic}},
				},
			},
			want: []string{"install disk", "network MAC"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.m.DiscoveryWarnings()
			if len(got) != len(tc.want) {
				t.Fatalf("DiscoveryWarnings() = %q, want %d warning(s) %q", got, len(tc.want), tc.want)
			}

			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Fatalf("warning %d = %q, want it to contain %q", i, got[i], w)
				}
			}
		})
	}
}

// TestDiskSelectorMatches pins which identifier of a selector is compared: the one it sets, against the
// same field of the discovered disk, never against an empty one.
func TestDiskSelectorMatches(t *testing.T) {
	d := TalosMachineDiscoveredDisk{Serial: "S1", DevPath: "/dev/sda"}

	if !DiskSelectorMatches(TalosMachineDisk{Serial: "S1"}, d) {
		t.Fatal("the serial must match")
	}

	if DiskSelectorMatches(TalosMachineDisk{WWID: "w"}, d) {
		t.Fatal("a WWID the disk did not report must not match an empty field")
	}

	if got := describeDiskSelector(TalosMachineDisk{DevPath: "/dev/sdb"}); got != "/dev/sdb" {
		t.Fatalf("describeDiskSelector = %q, want /dev/sdb", got)
	}
}
