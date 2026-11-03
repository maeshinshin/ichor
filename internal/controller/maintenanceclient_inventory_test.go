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

package controller

import (
	"context"
	"net"
	"testing"

	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The names the inventory tests reuse; goconst would otherwise flag each repeated literal.
const (
	inventoryDisk = "/dev/sda"
	inventoryNIC  = "eno1"
)

// newMaintenanceState is a COSI state with the two resource types ReadInventory lists.
func newMaintenanceState(t *testing.T) state.State {
	t.Helper()

	st := state.WrapCore(namespaced.NewState(inmem.Build))
	ctx := context.Background()

	for _, def := range []meta.ResourceDefinitionSpec{
		block.DiskExtension{}.ResourceDefinition(),
		network.LinkStatusExtension{}.ResourceDefinition(),
	} {
		d, err := meta.NewResourceDefinition(def)
		if err != nil {
			t.Fatal(err)
		}

		if err := st.Create(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	return st
}

func putDisk(t *testing.T, st state.State, id string, mutate func(*block.DiskSpec)) {
	t.Helper()

	d := block.NewDisk(block.NamespaceName, id)
	mutate(d.TypedSpec())

	if err := st.Create(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func putLink(t *testing.T, st state.State, name string, mutate func(*network.LinkStatusSpec)) {
	t.Helper()

	l := network.NewLinkStatus(network.NamespaceName, name)
	mutate(l.TypedSpec())

	if err := st.Create(context.Background(), l); err != nil {
		t.Fatal(err)
	}
}

// TestReadInventory pins what discovery records (ADR 0111): installable disks only, with the by-id
// symlink taken from the symlinks, sorted by device path; physical links with a permanent address only,
// sorted by name.
func TestReadInventory(t *testing.T) {
	st := newMaintenanceState(t)
	mac := func(s string) net.HardwareAddr {
		hw, err := net.ParseMAC(s)
		if err != nil {
			t.Fatal(err)
		}

		return hw
	}

	putDisk(t, st, "sdb", func(s *block.DiskSpec) {
		s.DevPath, s.Serial, s.Size, s.Model, s.Transport = testDevice, "S-B", 500<<30, "WD Blue", "sata"
		s.Symlinks = []string{"/dev/disk/by-path/x", "/dev/disk/by-id/ata-WD-B"}
	})
	putDisk(t, st, "sda", func(s *block.DiskSpec) {
		s.DevPath, s.WWID, s.BusPath, s.Size = inventoryDisk, "naa.1", "/pci0000:00/ata1", 120<<30
	})
	putDisk(t, st, "sr0", func(s *block.DiskSpec) { s.DevPath, s.CDROM = "/dev/sr0", true })
	putDisk(t, st, "loop0", func(s *block.DiskSpec) { s.DevPath, s.Readonly = "/dev/loop0", true })

	putLink(t, st, "enp3s0", func(s *network.LinkStatusSpec) {
		s.Type, s.PermanentAddr = nethelpers.LinkEther, nethelpers.HardwareAddr(mac("aa:bb:cc:00:00:02"))
		s.LinkState, s.SpeedMegabits, s.Driver = true, 1000, "igb"
	})
	putLink(t, st, inventoryNIC, func(s *network.LinkStatusSpec) {
		s.Type, s.PermanentAddr = nethelpers.LinkEther, nethelpers.HardwareAddr(mac("aa:bb:cc:00:00:01"))
	})
	putLink(t, st, "bond0", func(s *network.LinkStatusSpec) {
		s.Type, s.Kind, s.PermanentAddr = nethelpers.LinkEther, "bond", nethelpers.HardwareAddr(mac("aa:bb:cc:00:00:09"))
	})

	inv, err := ReadInventory(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}

	wantDisks := []ichorv1alpha1.TalosMachineDiscoveredDisk{
		{WWID: "naa.1", BusPath: "/pci0000:00/ata1", DevPath: inventoryDisk, SizeBytes: 120 << 30},
		{Serial: "S-B", ByIDPath: "/dev/disk/by-id/ata-WD-B", DevPath: testDevice, SizeBytes: 500 << 30, Model: "WD Blue", Transport: "sata"},
	}
	if len(inv.Disks) != len(wantDisks) {
		t.Fatalf("Disks = %+v, want %+v", inv.Disks, wantDisks)
	}

	for i := range wantDisks {
		if inv.Disks[i] != wantDisks[i] {
			t.Fatalf("Disks[%d] = %+v, want %+v", i, inv.Disks[i], wantDisks[i])
		}
	}

	wantLinks := []ichorv1alpha1.TalosMachineDiscoveredLink{
		{Name: inventoryNIC, PermanentMAC: "aa:bb:cc:00:00:01"},
		{Name: "enp3s0", PermanentMAC: "aa:bb:cc:00:00:02", LinkUp: true, SpeedMbit: 1000, Driver: "igb"},
	}
	if len(inv.Links) != len(wantLinks) {
		t.Fatalf("Links = %+v, want %+v", inv.Links, wantLinks)
	}

	for i := range wantLinks {
		if inv.Links[i] != wantLinks[i] {
			t.Fatalf("Links[%d] = %+v, want %+v", i, inv.Links[i], wantLinks[i])
		}
	}
}
