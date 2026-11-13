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
	"bytes"
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	testNIC        = "aa:bb:cc:dd:ee:ff"
	testGateway    = "10.0.0.1"
	testStaticAddr = "10.0.0.2/24"
	testVolume     = "data"
	testBadDisk    = "sda"
)

func TestNetworkFromFlags(t *testing.T) {
	good := machineApproveOptions{
		NIC: testNIC, Addresses: []string{testStaticAddr}, Gateway: testGateway, DNS: []string{testDNSPrimary}, VLAN: 20,
	}

	got, err := networkFromFlags(good)
	if err != nil {
		t.Fatal(err)
	}

	if got.GetAddressing() != "static" || got.GetMac() != testNIC || got.GetVlanId() != 20 || got.GetGateway() != testGateway {
		t.Fatalf("static flags: %+v", got)
	}

	got, err = networkFromFlags(machineApproveOptions{NIC: testNIC})
	if err != nil || got.GetAddressing() != "dhcp" {
		t.Fatalf("--nic alone is DHCP, got %+v, %v", got, err)
	}

	if got, err = networkFromFlags(machineApproveOptions{}); got != nil || err != nil {
		t.Fatalf("no flags: want nil, got %+v, %v", got, err)
	}

	for _, bad := range []machineApproveOptions{
		{Addresses: []string{testStaticAddr}},
		{Gateway: testGateway},
		{DNS: []string{testDNSPrimary}},
		{VLAN: 5},
		{NIC: testNIC, Gateway: testGateway},
	} {
		if _, err := networkFromFlags(bad); err == nil || !strings.Contains(err.Error(), "--nic") && !strings.Contains(err.Error(), "--address") {
			t.Errorf("networkFromFlags(%+v) must name the missing flag, got %v", bad, err)
		}
	}
}

func TestDisksFromFlags(t *testing.T) {
	got, err := disksFromFlags(machineApproveOptions{
		EphemeralDisk: "serial:E1",
		UserVolumes:   []string{testVolume + "=wwid:naa.1,size=200GiB", "scratch=/dev/disk/by-id/x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 || got[0].GetRole() != "ephemeral" || got[0].GetDisk().GetSerial() != "E1" ||
		got[1].GetName() != testVolume || got[1].GetMaxSize() != "200GiB" || got[1].GetDisk().GetWwid() != "naa.1" ||
		got[2].GetName() != "scratch" || got[2].GetMaxSize() != "" || got[2].GetDisk().GetByIdPath() != "/dev/disk/by-id/x" {
		t.Fatalf("disks: %+v", got)
	}

	for _, bad := range []machineApproveOptions{
		{EphemeralDisk: testBadDisk},
		{UserVolumes: []string{"nodisk"}},
		{UserVolumes: []string{"=serial:x"}},
		{UserVolumes: []string{"a=" + testBadDisk}},
	} {
		if _, err := disksFromFlags(bad); err == nil {
			t.Errorf("disksFromFlags(%+v) must fail", bad)
		}
	}
}

func TestApproveWritesNetworkAndDisks(t *testing.T) {
	setAPITestFlags(t, true)

	c, k8s := freshClients(t, []client.Object{strongMachine()})
	run := func(o machineApproveOptions) error {
		o.Role = testRoleWorker

		return runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong", o)
	}

	// A NIC the machine was not discovered with reaches the caller as InvalidArgument.
	if err := run(machineApproveOptions{NIC: "00:11:22:33:44:55"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown NIC: want InvalidArgument, got %v", err)
	}

	err := run(machineApproveOptions{
		NIC: testNIC, Addresses: []string{testStaticAddr}, Gateway: testGateway, DNS: []string{"9.9.9.9"},
		EphemeralDisk: "serial:E1", UserVolumes: []string{testVolume + "=wwid:naa.1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := getMachine(t, k8s, "strong").Spec
	if got.Network == nil || got.Network.Addressing != "static" || got.Network.MAC != testNIC || got.Network.Gateway != testGateway {
		t.Fatalf("spec.network: %+v", got.Network)
	}

	if len(got.Disks) != 2 || got.Disks[0].Role != "ephemeral" || got.Disks[1].Name != testVolume {
		t.Fatalf("spec.disks: %+v", got.Disks)
	}
}

// --nic takes a link name as well as a MAC: the CLI passes it on as given and the API resolves it against the links
// the node reported, so the same value works on every client.
func TestNetworkFromFlags_NICMayBeALinkName(t *testing.T) {
	n, err := networkFromFlags(machineApproveOptions{NIC: testHWLink})
	if err != nil || n.GetMac() != testHWLink {
		t.Fatalf("got %+v, %v", n, err)
	}
}
