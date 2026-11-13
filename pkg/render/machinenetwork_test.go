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

package render

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	testNICMAC   = "AA:BB:CC:DD:EE:01"
	contractV111 = "v1.11"
)

// renderWith renders a worker at the given contract ("" is the cluster default, v1.14) after
// mutating the machine.
func renderWith(t *testing.T, contract string, mutate func(m *ichorv1alpha1.TalosMachine)) string {
	t.Helper()

	cluster := testCluster("true")
	if contract != "" {
		cluster.Spec.VersionContract = contract
	}

	m := testMachine("node-a", roleWorker, nil)
	mutate(m)

	return mustRender(t, cluster, m, testBundle(t), nil)
}

// TestRenderNetwork: spec.network picks the primary link by permanent MAC through a
// LinkAliasConfig and refers to the alias from the addressing documents. v1.13 and
// v1.14 both use documents; v1.11 is the legacy machine.network.interfaces form.
func TestRenderNetwork(t *testing.T) {
	dhcp := func(m *ichorv1alpha1.TalosMachine) {
		m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{MAC: testNICMAC, Addressing: "dhcp"}
	}
	static := func(m *ichorv1alpha1.TalosMachine) {
		m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{
			MAC: testNICMAC, Addressing: "static", Addresses: []string{"192.168.1.10/24"},
			Gateway: "192.168.1.1", DNSServers: []string{"1.1.1.1", "9.9.9.9"},
		}
	}
	vlan := func(m *ichorv1alpha1.TalosMachine) {
		static(m)
		m.Spec.Network.VLANID = 100
	}

	for _, contract := range []string{"", contractV113} {
		name := contract
		if name == "" {
			name = versionContract
		}

		alias := []string{"kind: LinkAliasConfig", "name: net0", `mac(link.permanent_addr) == "aa:bb:cc:dd:ee:01"`}

		t.Run(name+"/dhcp", func(t *testing.T) {
			out := renderWith(t, contract, dhcp)
			requireAll(t, out, append(alias, "kind: DHCPv4Config")...)
			requireNone(t, out, "kind: LinkConfig", "kind: VLANConfig", "192.168.1.10")
		})

		t.Run(name+"/static", func(t *testing.T) {
			out := renderWith(t, contract, static)
			requireAll(t, out, append(alias, "kind: LinkConfig", "address: 192.168.1.10/24", "gateway: 192.168.1.1",
				"kind: ResolverConfig", "address: 1.1.1.1", "address: 9.9.9.9")...)
			requireNone(t, out, "kind: DHCPv4Config", "kind: VLANConfig")
		})

		t.Run(name+"/static on a vlan", func(t *testing.T) {
			out := renderWith(t, contract, vlan)
			requireAll(t, out, append(alias, "kind: VLANConfig", "name: net0.100", "vlanID: 100", "parent: net0",
				"address: 192.168.1.10/24")...)
			requireNone(t, out, "kind: DHCPv4Config")
		})

		t.Run(name+"/dhcp on a vlan", func(t *testing.T) {
			out := renderWith(t, contract, func(m *ichorv1alpha1.TalosMachine) {
				dhcp(m)
				m.Spec.Network.VLANID = 7
			})
			requireAll(t, out, "kind: VLANConfig", "name: net0.7", "kind: DHCPv4Config")
		})

		t.Run(name+"/unset renders nothing", func(t *testing.T) {
			requireNone(t, renderWith(t, contract, func(*ichorv1alpha1.TalosMachine) {}), "LinkAliasConfig", "net0")
		})
	}

	t.Run("v1.11 legacy", func(t *testing.T) {
		out := renderWith(t, contractV111, static)
		requireAll(t, out, "interfaces:", "permanentAddr: aa:bb:cc:dd:ee:01", "192.168.1.10/24", "gateway: 192.168.1.1",
			"network: 0.0.0.0/0", "nameservers:", "1.1.1.1")
		requireNone(t, out, "LinkAliasConfig")

		out = renderWith(t, contractV111, vlan)
		requireAll(t, out, "vlans:", "vlanId: 100", "192.168.1.10/24")

		out = renderWith(t, contractV111, dhcp)
		requireAll(t, out, "permanentAddr: aa:bb:cc:dd:ee:01", "dhcp: true")
	})

	t.Run("an IPv6 gateway routes ::/0 in the legacy form", func(t *testing.T) {
		out := renderWith(t, contractV111, func(m *ichorv1alpha1.TalosMachine) {
			m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{
				MAC: testNICMAC, Addressing: "static", Addresses: []string{"fd00::10/64"}, Gateway: "fd00::1",
			}
		})
		requireAll(t, out, "network: ::/0", "gateway: fd00::1")
	})
}

// TestRenderDisks: spec.disks becomes a VolumeConfig for EPHEMERAL and a UserVolumeConfig per
// user volume, each with a CEL disk selector.
func TestRenderDisks(t *testing.T) {
	disks := func(m *ichorv1alpha1.TalosMachine) {
		m.Spec.Disks = []ichorv1alpha1.TalosMachineDiskRole{
			{Role: "ephemeral", Disk: ichorv1alpha1.TalosMachineDisk{Serial: "EPH1"}},
			{Role: "userVolume", Name: "data", MaxSize: "200GiB", Disk: ichorv1alpha1.TalosMachineDisk{WWID: "naa.1"}},
			{Role: "userVolume", Name: "scratch", Disk: ichorv1alpha1.TalosMachineDisk{ByIDPath: "/dev/disk/by-id/x"}},
		}
	}

	for _, contract := range []string{"", contractV113} {
		name := contract
		if name == "" {
			name = versionContract
		}

		t.Run(name, func(t *testing.T) {
			out := renderWith(t, contract, disks)
			requireAll(t, out,
				"kind: VolumeConfig", "name: EPHEMERAL", `disk.serial == "EPH1"`,
				"kind: UserVolumeConfig", "name: data", `disk.wwid == "naa.1"`, "maxSize: 200GiB",
				"name: scratch", `"/dev/disk/by-id/x" in disk.symlinks`, "minSize: 1GiB", "grow: true")
		})
	}

	t.Run("unset renders no volume documents", func(t *testing.T) {
		requireNone(t, renderWith(t, "", func(*ichorv1alpha1.TalosMachine) {}), "kind: UserVolumeConfig", "diskSelector")
	})
}

// TestRenderVIP: a control plane of a vip cluster that has a primary link gets the endpoint address as a
// Layer2VIPConfig on that link, next to the net0 alias that names it (net0.<id> on a VLAN); a worker, a machine
// without spec.network and a cluster of any other endpoint type get none.
func TestRenderVIP(t *testing.T) {
	dhcp := func(m *ichorv1alpha1.TalosMachine) {
		m.Spec.Role = roleControlPlane
		m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{MAC: testNICMAC, Addressing: ichorv1alpha1.MachineAddressingDHCP}
	}
	render := func(t *testing.T, contract, endpointType string, mutate func(*ichorv1alpha1.TalosMachine)) string {
		t.Helper()

		cluster := testCluster("true")
		cluster.Spec.Endpoint.Type = endpointType

		if contract != "" {
			cluster.Spec.VersionContract = contract
		}

		m := testMachine("node-a", roleWorker, nil)
		mutate(m)

		return mustRender(t, cluster, m, testBundle(t), nil)
	}

	t.Run("control plane: the VIP and the alias it names come together", func(t *testing.T) {
		out := render(t, "", "vip", dhcp)

		// The two documents are pinned whole and side by side: the VIP's link is the alias's name, so neither
		// can change without this failing.
		if got, want := documentOfKind(t, out, "Layer2VIPConfig"),
			"apiVersion: v1alpha1\nkind: Layer2VIPConfig\nname: 10.0.0.5\nlink: net0\n"; got != want {
			t.Errorf("Layer2VIPConfig =\n%s\nwant\n%s", got, want)
		}

		if got, want := documentOfKind(t, out, "LinkAliasConfig"),
			"apiVersion: v1alpha1\nkind: LinkAliasConfig\nname: net0\nselector:\n"+
				"    match: mac(link.permanent_addr) == \"aa:bb:cc:dd:ee:01\"\n"; got != want {
			t.Errorf("LinkAliasConfig =\n%s\nwant\n%s", got, want)
		}

		requireAll(t, out, "kind: DHCPv4Config")
	})

	t.Run("control plane on a vlan: the VIP is on the vlan link", func(t *testing.T) {
		out := render(t, "", "vip", func(m *ichorv1alpha1.TalosMachine) {
			dhcp(m)
			m.Spec.Network.VLANID = 100
		})
		requireAll(t, out, "kind: Layer2VIPConfig", "name: 10.0.0.5", "link: net0.100", "kind: VLANConfig")
	})

	t.Run("worker: none", func(t *testing.T) {
		out := render(t, "", "vip", func(m *ichorv1alpha1.TalosMachine) {
			dhcp(m)
			m.Spec.Role = roleWorker
		})
		requireNone(t, out, "Layer2VIPConfig")
		requireAll(t, out, "kind: LinkAliasConfig")
	})

	t.Run("no spec.network: none", func(t *testing.T) {
		requireNone(t, render(t, "", "vip", func(m *ichorv1alpha1.TalosMachine) { m.Spec.Role = roleControlPlane }),
			"Layer2VIPConfig", "net0")
	})

	for _, typ := range []string{"node", "loadBalancer"} {
		t.Run("endpoint type "+typ+": none", func(t *testing.T) {
			requireNone(t, render(t, "", typ, dhcp), "Layer2VIPConfig", "vip:")
		})
	}

	t.Run("v1.11 legacy: vip on the selected device", func(t *testing.T) {
		out := render(t, contractV111, "vip", dhcp)
		requireAll(t, out, "permanentAddr: aa:bb:cc:dd:ee:01", "vip:", "ip: 10.0.0.5")
		requireNone(t, out, "Layer2VIPConfig")

		out = render(t, contractV111, "vip", func(m *ichorv1alpha1.TalosMachine) {
			dhcp(m)
			m.Spec.Network.VLANID = 100
		})
		requireAll(t, out, "vlans:", "vip:", "ip: 10.0.0.5")
	})
}

// documentOfKind is the one document of the rendered multi-document configuration with the given kind.
func documentOfKind(t *testing.T, out, kind string) string {
	t.Helper()

	var found []string

	for doc := range strings.SplitSeq(out, "\n---\n") {
		if strings.Contains(doc, "\nkind: "+kind+"\n") {
			found = append(found, strings.TrimSpace(doc)+"\n")
		}
	}

	if len(found) != 1 {
		t.Fatalf("%d documents of kind %s, want 1", len(found), kind)
	}

	return found[0]
}
