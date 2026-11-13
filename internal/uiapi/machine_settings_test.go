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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	nic1       = "aa:bb:cc:dd:ee:01"
	linkEno1   = "eno1"
	linkEno2   = "eno2"
	linkTwin   = "twin"
	nic2       = "aa:bb:cc:dd:ee:02"
	staticAddr = "10.0.0.2/24"
)

func TestNetworkFromPB(t *testing.T) {
	id := ichorv1alpha1.TalosMachineIdentity{MACAddresses: []string{nic1, "AA:BB:CC:DD:EE:02"}}

	links := []ichorv1alpha1.TalosMachineDiscoveredLink{
		{Name: linkEno1, PermanentMAC: nic1}, {Name: linkEno2, PermanentMAC: nic2},
		{Name: linkTwin, PermanentMAC: nic1}, {Name: linkTwin, PermanentMAC: nic2},
	}

	cases := []struct {
		name    string
		in      *pb.MachineNetwork
		wantErr string
	}{
		{"none", nil, ""},
		{"dhcp", &pb.MachineNetwork{Mac: nic1}, ""},
		{"the MAC is matched case-insensitively", &pb.MachineNetwork{Mac: nic2}, ""},
		{ichorv1alpha1.MachineAddressingStatic, &pb.MachineNetwork{
			Mac: nic1, Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{staticAddr},
			Gateway: gatewayIP, DnsServers: []string{"1.1.1.1"}, VlanId: 20,
		}, ""},
		{"a MAC the machine does not have", &pb.MachineNetwork{Mac: "aa:bb:cc:dd:ee:99"}, "discovered MAC"},
		{"a MAC that is not written with colons", &pb.MachineNetwork{Mac: "aa-bb-cc-dd-ee-01"}, "not a MAC address"},
		{"a link name resolves to its MAC", &pb.MachineNetwork{Mac: linkEno1}, ""},
		{"a link name that no reported link has", &pb.MachineNetwork{Mac: "eth9"}, linkEno1 + " <" + nic1 + ">\n  " + linkEno2 + " <" + nic2 + ">"},
		{"a link name two reported links share", &pb.MachineNetwork{Mac: linkTwin}, "names 2 links"},
		{"static with no address", &pb.MachineNetwork{Mac: nic1, Addressing: ichorv1alpha1.MachineAddressingStatic}, "at least one address"},
		{"dhcp with an address", &pb.MachineNetwork{Mac: nic1, Addresses: []string{staticAddr}}, "dhcp takes no"},
		{"an address with no prefix", &pb.MachineNetwork{
			Mac: nic1, Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{"10.0.0.2"},
		}, "prefix length"},
		{"a bad gateway", &pb.MachineNetwork{
			Mac: nic1, Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{staticAddr}, Gateway: "gw",
		}, "gateway"},
		{"a bad DNS server", &pb.MachineNetwork{Mac: nic1, DnsServers: []string{"dns"}}, "dns_servers"},
		{"a VLAN out of range", &pb.MachineNetwork{Mac: nic1, VlanId: 4095}, "vlan_id"},
		{"an unknown addressing", &pb.MachineNetwork{Mac: nic1, Addressing: "slaac"}, "dhcp or static"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NetworkFromPB(tc.in, id, links)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			case tc.wantErr == "" && tc.in != nil && got.Addressing == "":
				t.Fatalf("addressing must default to dhcp: %+v", got)
			}
		})
	}
}

func TestNetworkRoundTrip(t *testing.T) {
	in := &pb.MachineNetwork{
		Mac: "AA:BB:CC:DD:EE:01", Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{staticAddr}, Gateway: gatewayIP,
		DnsServers: []string{"9.9.9.9"}, VlanId: 5,
	}

	spec, err := NetworkFromPB(in, ichorv1alpha1.TalosMachineIdentity{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	out := networkToPB(spec)
	if out.GetMac() != nic1 || out.GetGateway() != gatewayIP || out.GetVlanId() != 5 ||
		len(out.GetAddresses()) != 1 || len(out.GetDnsServers()) != 1 {
		t.Fatalf("round trip lost data: %+v", out)
	}
}

func TestDisksFromPB(t *testing.T) {
	disk := func(serial string) *pb.MachineDisk { return &pb.MachineDisk{Serial: serial} }

	cases := []struct {
		name    string
		in      []*pb.MachineDiskRole
		wantErr string
	}{
		{"none", nil, ""},
		{"ephemeral and a user volume", []*pb.MachineDiskRole{
			{Role: ichorv1alpha1.MachineDiskRoleEphemeral, Disk: disk("E")},
			{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "data", MaxSize: "100GiB", Disk: disk("D")},
		}, ""},
		{"two ephemeral", []*pb.MachineDiskRole{
			{Role: ichorv1alpha1.MachineDiskRoleEphemeral, Disk: disk("E")}, {Role: ichorv1alpha1.MachineDiskRoleEphemeral, Disk: disk("F")},
		}, "only one"},
		{"a repeated name", []*pb.MachineDiskRole{
			{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "a", Disk: disk("1")}, {Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "a", Disk: disk("2")},
		}, "twice"},
		{"no name", []*pb.MachineDiskRole{{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Disk: disk("1")}}, "name"},
		{"a name on ephemeral", []*pb.MachineDiskRole{{Role: ichorv1alpha1.MachineDiskRoleEphemeral, Name: "x", Disk: disk("1")}}, "no name or size"},
		{"a bad size", []*pb.MachineDiskRole{{Role: ichorv1alpha1.MachineDiskRoleUserVolume, Name: "a", MaxSize: "big", Disk: disk("1")}}, "max_size"},
		{"an unknown role", []*pb.MachineDiskRole{{Role: "storage", Disk: disk("1")}}, "ephemeral or userVolume"},
		{"no disk", []*pb.MachineDiskRole{{Role: ichorv1alpha1.MachineDiskRoleEphemeral}}, "exactly one"},
		{"two selectors", []*pb.MachineDiskRole{{Role: ichorv1alpha1.MachineDiskRoleEphemeral, Disk: &pb.MachineDisk{Serial: "a", Wwid: "b"}}}, "exactly one"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := disksFromPB(tc.in)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			case tc.wantErr == "" && len(disksToPB(got)) != len(tc.in):
				t.Fatalf("round trip changed the count: %d vs %d", len(disksToPB(got)), len(tc.in))
			}
		})
	}
}

// A link given by name is resolved against the links the node reported, and what is stored is the MAC: a kernel
// link name is not stable across boots and must never reach spec.network.
func TestNetworkFromPB_LinkNameIsResolvedToItsMAC(t *testing.T) {
	links := []ichorv1alpha1.TalosMachineDiscoveredLink{{Name: linkEno1, PermanentMAC: nic1}, {Name: linkEno2, PermanentMAC: "AA:BB:CC:DD:EE:02"}}

	got, err := NetworkFromPB(&pb.MachineNetwork{Mac: linkEno2}, ichorv1alpha1.TalosMachineIdentity{}, links)
	if err != nil || got.MAC != nic2 {
		t.Fatalf("got %+v, %v, want the MAC of eno2", got, err)
	}

	// A MAC keeps its path untouched, even with links that could be searched.
	if got, err = NetworkFromPB(&pb.MachineNetwork{Mac: "AA:BB:CC:DD:EE:01"}, ichorv1alpha1.TalosMachineIdentity{}, links); err != nil || got.MAC != nic1 {
		t.Fatalf("a MAC: got %+v, %v", got, err)
	}

	// No reported links: a name cannot be resolved, and the refusal says so.
	if _, err = NetworkFromPB(&pb.MachineNetwork{Mac: linkEno1}, ichorv1alpha1.TalosMachineIdentity{}, nil); err == nil ||
		!strings.Contains(err.Error(), "reported no links") {
		t.Fatalf("err = %v", err)
	}
}
