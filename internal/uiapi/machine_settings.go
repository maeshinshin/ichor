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

package uiapi

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var (
	macRe        = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)
	volumeNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)
	maxSizeRe    = regexp.MustCompile(`^[1-9][0-9]*(MiB|GiB|TiB)$`)
)

// networkFromPB converts the wire network settings to spec.network and checks them
// the way the CRD does, so the caller learns the reason instead of an admission error. The MAC
// must be one the machine was discovered with: that is what "permanent MAC discovered earlier"
// means, and it stops a typo from selecting a link that does not exist. nil means "none".
func networkFromPB(in *pb.MachineNetwork, identity ichorv1alpha1.TalosMachineIdentity) (*ichorv1alpha1.TalosMachineNetwork, error) {
	if in == nil {
		return nil, nil
	}

	if !macRe.MatchString(in.GetMac()) {
		return nil, fmt.Errorf("network.mac %q is not a MAC address like aa:bb:cc:dd:ee:ff", in.GetMac())
	}

	if len(identity.MACAddresses) > 0 && !containsMAC(identity.MACAddresses, in.GetMac()) {
		return nil, fmt.Errorf("network.mac %s is not one of the machine's discovered MAC addresses (%s)",
			in.GetMac(), strings.Join(identity.MACAddresses, ", "))
	}

	addressing := in.GetAddressing()
	if addressing == "" {
		addressing = ichorv1alpha1.MachineAddressingDHCP
	}

	switch addressing {
	case ichorv1alpha1.MachineAddressingDHCP:
		if len(in.GetAddresses()) > 0 || in.GetGateway() != "" {
			return nil, fmt.Errorf("network: dhcp takes no addresses or gateway")
		}
	case ichorv1alpha1.MachineAddressingStatic:
		if len(in.GetAddresses()) == 0 {
			return nil, fmt.Errorf("network: static addressing needs at least one address")
		}
	default:
		return nil, fmt.Errorf("network.addressing %q must be dhcp or static", addressing)
	}

	for _, a := range in.GetAddresses() {
		if _, err := netip.ParsePrefix(a); err != nil {
			return nil, fmt.Errorf("network.addresses %q needs a prefix length, like 192.168.1.10/24", a)
		}
	}

	if g := in.GetGateway(); g != "" {
		if _, err := netip.ParseAddr(g); err != nil {
			return nil, fmt.Errorf("network.gateway %q is not an IP address", g)
		}
	}

	for _, d := range in.GetDnsServers() {
		if _, err := netip.ParseAddr(d); err != nil {
			return nil, fmt.Errorf("network.dns_servers %q is not an IP address", d)
		}
	}

	if v := in.GetVlanId(); v < 0 || v > 4094 {
		return nil, fmt.Errorf("network.vlan_id %d must be between 1 and 4094 (0 for none)", v)
	}

	return &ichorv1alpha1.TalosMachineNetwork{
		MAC: strings.ToLower(in.GetMac()), Addressing: addressing, Addresses: in.GetAddresses(),
		Gateway: in.GetGateway(), DNSServers: in.GetDnsServers(), VLANID: in.GetVlanId(),
	}, nil
}

func containsMAC(macs []string, mac string) bool {
	want, err := net.ParseMAC(mac)
	if err != nil {
		return false
	}

	for _, m := range macs {
		if got, err := net.ParseMAC(m); err == nil && got.String() == want.String() {
			return true
		}
	}

	return false
}

func networkToPB(in *ichorv1alpha1.TalosMachineNetwork) *pb.MachineNetwork {
	if in == nil {
		return nil
	}

	return &pb.MachineNetwork{
		Mac: in.MAC, Addressing: in.Addressing, Addresses: in.Addresses, Gateway: in.Gateway,
		DnsServers: in.DNSServers, VlanId: in.VLANID,
	}
}

// disksFromPB converts the wire disk roles to spec.disks, with the CRD's rules: one
// selector per disk, at most one ephemeral disk, a unique name per user volume.
func disksFromPB(in []*pb.MachineDiskRole) ([]ichorv1alpha1.TalosMachineDiskRole, error) {
	var (
		out       []ichorv1alpha1.TalosMachineDiskRole
		ephemeral bool
		names     = map[string]bool{}
	)

	for i, d := range in {
		sel := d.GetDisk()

		set := 0

		for _, v := range []string{sel.GetWwid(), sel.GetSerial(), sel.GetByIdPath(), sel.GetBusPath(), sel.GetDevPath()} {
			if v != "" {
				set++
			}
		}

		if set != 1 {
			return nil, fmt.Errorf("disks[%d]: the disk needs exactly one of wwid, serial, by_id_path, bus_path or dev_path", i)
		}

		role := ichorv1alpha1.TalosMachineDiskRole{
			Role: d.GetRole(),
			Disk: ichorv1alpha1.TalosMachineDisk{
				WWID: sel.GetWwid(), Serial: sel.GetSerial(), ByIDPath: sel.GetByIdPath(),
				BusPath: sel.GetBusPath(), DevPath: sel.GetDevPath(),
			},
			Name: d.GetName(), MaxSize: d.GetMaxSize(),
		}

		switch d.GetRole() {
		case ichorv1alpha1.MachineDiskRoleEphemeral:
			if ephemeral {
				return nil, fmt.Errorf("disks[%d]: only one disk can have the ephemeral role", i)
			}

			ephemeral = true

			if d.GetName() != "" || d.GetMaxSize() != "" {
				return nil, fmt.Errorf("disks[%d]: an ephemeral disk takes no name or size", i)
			}
		case ichorv1alpha1.MachineDiskRoleUserVolume:
			if !volumeNameRe.MatchString(d.GetName()) {
				return nil, fmt.Errorf("disks[%d]: a user volume name is 1 to 32 letters, digits or hyphens", i)
			}

			if names[d.GetName()] {
				return nil, fmt.Errorf("disks[%d]: the user volume name %q is used twice", i, d.GetName())
			}

			names[d.GetName()] = true

			if d.GetMaxSize() != "" && !maxSizeRe.MatchString(d.GetMaxSize()) {
				return nil, fmt.Errorf("disks[%d]: max_size %q must look like 200GiB (MiB, GiB or TiB)", i, d.GetMaxSize())
			}
		default:
			return nil, fmt.Errorf("disks[%d]: role %q must be ephemeral or userVolume", i, d.GetRole())
		}

		out = append(out, role)
	}

	return out, nil
}

// discoveredDisksToPB mirrors TalosMachine.status.discoveredDisks; nil when none was recorded.
func discoveredDisksToPB(in []ichorv1alpha1.TalosMachineDiscoveredDisk) []*pb.DiscoveredDisk {
	var out []*pb.DiscoveredDisk

	for _, d := range in {
		out = append(out, &pb.DiscoveredDisk{
			Wwid: d.WWID, Serial: d.Serial, ByIdPath: d.ByIDPath, BusPath: d.BusPath, DevPath: d.DevPath,
			SizeBytes: d.SizeBytes, Model: d.Model, Transport: d.Transport,
		})
	}

	return out
}

// discoveredLinksToPB mirrors TalosMachine.status.discoveredLinks; nil when none was recorded.
func discoveredLinksToPB(in []ichorv1alpha1.TalosMachineDiscoveredLink) []*pb.DiscoveredLink {
	var out []*pb.DiscoveredLink

	for _, l := range in {
		out = append(out, &pb.DiscoveredLink{
			Name: l.Name, PermanentMac: l.PermanentMAC, LinkUp: l.LinkUp, SpeedMbit: l.SpeedMbit, Driver: l.Driver,
		})
	}

	return out
}

func disksToPB(in []ichorv1alpha1.TalosMachineDiskRole) []*pb.MachineDiskRole {
	out := make([]*pb.MachineDiskRole, 0, len(in))

	for _, d := range in {
		out = append(out, &pb.MachineDiskRole{
			Role: d.Role, Name: d.Name, MaxSize: d.MaxSize,
			Disk: &pb.MachineDisk{
				Wwid: d.Disk.WWID, Serial: d.Disk.Serial, ByIdPath: d.Disk.ByIDPath,
				BusPath: d.Disk.BusPath, DevPath: d.Disk.DevPath,
			},
		})
	}

	return out
}
