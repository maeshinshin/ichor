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

// LinkLabel is how a link is shown to a person: its name and its MAC in angle brackets, enp1s0 <84:47:09:5c:ed:49>.
// Both are values --nic takes; the MAC is kept as discovered, with its colons and lower case.
func LinkLabel(l ichorv1alpha1.TalosMachineDiscoveredLink) string {
	return l.Name + " <" + l.PermanentMAC + ">"
}

// ResolveLink turns a link given as a name into the permanent MAC of the link of that name among the links the node
// reported in discovery (status.discoveredLinks); anything that parses as a MAC is returned unchanged. The name is
// input convenience only: kernel link names are not stable across boots, so only the MAC is ever stored and rendered.
// A name that matches no link, or more than one, is refused, and the refusal lists every link with its name and MAC.
func ResolveLink(nic string, links []ichorv1alpha1.TalosMachineDiscoveredLink) (string, error) {
	if _, err := net.ParseMAC(nic); err == nil {
		return nic, nil
	}

	var matches []string

	for _, l := range links {
		if l.Name == nic {
			matches = append(matches, l.PermanentMAC)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	have := "the node has reported no links"

	if len(links) > 0 {
		listed := make([]string, len(links))
		for i, l := range links {
			listed[i] = "  " + LinkLabel(l)
		}

		have = "its links:\n" + strings.Join(listed, "\n")
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("link %q names %d links of the node, so it cannot say which; give the MAC. %s", nic, len(matches), have)
	}

	return "", fmt.Errorf("link %q is neither a MAC address nor the name of a link the node reported; %s", nic, have)
}

// NetworkFromPB converts the wire network settings to spec.network and checks them
// the way the CRD does, so the caller learns the reason instead of an admission error. The MAC
// must be one the machine was discovered with: that is what "permanent MAC discovered earlier"
// means, and it stops a typo from selecting a link that does not exist. nil means "none". The link may be given by name (see ResolveLink); the result always holds its MAC.
func NetworkFromPB(
	in *pb.MachineNetwork, identity ichorv1alpha1.TalosMachineIdentity, links []ichorv1alpha1.TalosMachineDiscoveredLink,
) (*ichorv1alpha1.TalosMachineNetwork, error) {
	if in == nil {
		return nil, nil
	}

	mac, err := ResolveLink(in.GetMac(), links)
	if err != nil {
		return nil, err
	}

	if !macRe.MatchString(mac) {
		return nil, fmt.Errorf("network.mac %q is not a MAC address like aa:bb:cc:dd:ee:ff", mac)
	}

	if len(identity.MACAddresses) > 0 && !containsMAC(identity.MACAddresses, mac) {
		return nil, fmt.Errorf("network.mac %s is not one of the node's discovered MAC addresses (%s)",
			mac, strings.Join(identity.MACAddresses, ", "))
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
		MAC: strings.ToLower(mac), Addressing: addressing, Addresses: in.GetAddresses(),
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

// diskFromPB converts a wire disk selector; exactly one identifier must be set, the same rule the CRD enforces.
func diskFromPB(who string, d *pb.MachineDisk) (ichorv1alpha1.TalosMachineDisk, error) {
	set := 0

	for _, v := range []string{d.GetWwid(), d.GetSerial(), d.GetByIdPath(), d.GetBusPath(), d.GetDevPath()} {
		if v != "" {
			set++
		}
	}

	if set != 1 {
		return ichorv1alpha1.TalosMachineDisk{}, fmt.Errorf("%s needs exactly one of wwid, serial, by_id_path, bus_path or dev_path", who)
	}

	return ichorv1alpha1.TalosMachineDisk{
		WWID: d.GetWwid(), Serial: d.GetSerial(), ByIDPath: d.GetByIdPath(), BusPath: d.GetBusPath(), DevPath: d.GetDevPath(),
	}, nil
}

func diskToPB(d ichorv1alpha1.TalosMachineDisk) *pb.MachineDisk {
	return &pb.MachineDisk{Wwid: d.WWID, Serial: d.Serial, ByIdPath: d.ByIDPath, BusPath: d.BusPath, DevPath: d.DevPath}
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
		disk, err := diskFromPB(fmt.Sprintf("disks[%d]: the disk", i), d.GetDisk())
		if err != nil {
			return nil, err
		}

		role := ichorv1alpha1.TalosMachineDiskRole{Role: d.GetRole(), Disk: disk, Name: d.GetName(), MaxSize: d.GetMaxSize()}

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
	out := make([]*pb.DiscoveredDisk, 0, len(in))

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
	out := make([]*pb.DiscoveredLink, 0, len(in))

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
			Role: d.Role, Name: d.Name, MaxSize: d.MaxSize, Disk: diskToPB(d.Disk),
		})
	}

	return out
}
