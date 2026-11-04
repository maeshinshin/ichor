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
	"fmt"
	"net/netip"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/cel"
	"github.com/siderolabs/talos/pkg/machinery/cel/celenv"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/block"
	"github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// primaryLinkAlias is the name the primary link gets from its LinkAliasConfig; every other
// network document refers to it, so a kernel rename of the interface changes nothing.
const primaryLinkAlias = "net0"

// ephemeralVolume is the name of the EPHEMERAL system volume.
const ephemeralVolume = "EPHEMERAL"

// injectNetwork renders spec.network. From the multi-document contract it is a
// LinkAliasConfig whose CEL selector matches the permanent MAC, plus a DHCPv4Config or a
// LinkConfig (or, with a VLAN, a VLANConfig on the alias) for the addressing and a
// ResolverConfig for the DNS servers. Before it, one machine.network.interfaces entry selected by
// permanent address.
func injectNetwork(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, m *ichorv1alpha1.TalosMachine,
) (coreconfig.Provider, error) {
	n := m.Spec.Network
	if n == nil {
		return cfg, nil
	}

	routes, err := networkRoutes(n)
	if err != nil {
		return nil, err
	}

	if !contract.MultidocNetworkConfigSupported() {
		return injectLegacyNetwork(cfg, n, routes)
	}

	match, err := cel.ParseBooleanExpression(
		fmt.Sprintf("mac(link.permanent_addr) == %q", strings.ToLower(n.MAC)), celenv.LinkLocator())
	if err != nil {
		return nil, fmt.Errorf("render: network.mac selector: %w", err)
	}

	alias := network.NewLinkAliasConfigV1Alpha1(primaryLinkAlias)
	alias.Selector.Match = match

	if cfg, err = mergeDocument(cfg, "link alias", alias); err != nil {
		return nil, err
	}

	addrs := make([]network.AddressConfig, 0, len(n.Addresses))

	for _, a := range n.Addresses {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return nil, fmt.Errorf("render: network.addresses %q: %w", a, err)
		}

		addrs = append(addrs, network.AddressConfig{AddressAddress: p})
	}

	common := network.CommonLinkConfig{LinkUp: new(true)}
	target := primaryLinkAlias

	if n.VLANID != 0 {
		// The parent carries nothing but being up; the addressing goes on the VLAN link.
		parent := network.NewLinkConfigV1Alpha1(primaryLinkAlias)
		parent.LinkUp = new(true)

		if cfg, err = mergeDocument(cfg, "vlan parent link", parent); err != nil {
			return nil, err
		}

		target = fmt.Sprintf("%s.%d", primaryLinkAlias, n.VLANID)
		vlan := network.NewVLANConfigV1Alpha1(target)
		vlan.VLANIDConfig = uint16(n.VLANID) //nolint:gosec // the CRD bounds it to 1..4094
		vlan.ParentLinkConfig = primaryLinkAlias
		vlan.CommonLinkConfig = common

		if n.Addressing == ichorv1alpha1.MachineAddressingStatic {
			vlan.LinkAddresses, vlan.LinkRoutes = addrs, routes
		}

		if cfg, err = mergeDocument(cfg, "vlan", vlan); err != nil {
			return nil, err
		}
	} else if n.Addressing == ichorv1alpha1.MachineAddressingStatic {
		link := network.NewLinkConfigV1Alpha1(primaryLinkAlias)
		link.CommonLinkConfig = common
		link.LinkAddresses, link.LinkRoutes = addrs, routes

		if cfg, err = mergeDocument(cfg, "link", link); err != nil {
			return nil, err
		}
	}

	if n.Addressing != ichorv1alpha1.MachineAddressingStatic {
		if cfg, err = mergeDocument(cfg, "dhcp", network.NewDHCPv4ConfigV1Alpha1(target)); err != nil {
			return nil, err
		}
	}

	if len(n.DNSServers) == 0 {
		return cfg, nil
	}

	resolver := network.NewResolverConfigV1Alpha1()

	for _, s := range n.DNSServers {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("render: network.dnsServers %q: %w", s, err)
		}

		resolver.ResolverNameservers = append(resolver.ResolverNameservers,
			network.NameserverConfig{Address: meta.Addr{Addr: a}})
	}

	return mergeDocument(cfg, "resolver", resolver)
}

// networkRoutes is the default route through spec.network.gateway, or none.
func networkRoutes(n *ichorv1alpha1.TalosMachineNetwork) ([]network.RouteConfig, error) {
	if n.Gateway == "" {
		return nil, nil
	}

	gw, err := netip.ParseAddr(n.Gateway)
	if err != nil {
		return nil, fmt.Errorf("render: network.gateway %q: %w", n.Gateway, err)
	}

	return []network.RouteConfig{{RouteGateway: meta.Addr{Addr: gw}}}, nil
}

// injectLegacyNetwork is the pre-multi-document form: one interface selected by permanent
// address, with the VLAN nested under it, and the DNS servers under machine.network.
//
//nolint:staticcheck // the legacy block is the only form a 1.11 or older contract accepts
func injectLegacyNetwork(
	cfg coreconfig.Provider, n *ichorv1alpha1.TalosMachineNetwork, gateway []network.RouteConfig,
) (coreconfig.Provider, error) {
	dev := &mv1alpha1.Device{
		DeviceSelector: &mv1alpha1.NetworkDeviceSelector{NetworkDevicePermanentAddress: strings.ToLower(n.MAC)},
	}

	static := n.Addressing == ichorv1alpha1.MachineAddressingStatic

	var routes []*mv1alpha1.Route

	// gateway came from networkRoutes, which already parsed (and refused a bad) spec.network.gateway.
	if len(gateway) > 0 {
		dest := "0.0.0.0/0"
		if gateway[0].RouteGateway.Is6() {
			dest = "::/0"
		}

		routes = []*mv1alpha1.Route{{RouteNetwork: dest, RouteGateway: n.Gateway}}
	}

	if n.VLANID != 0 {
		vlan := &mv1alpha1.Vlan{VlanID: uint16(n.VLANID)} //nolint:gosec // the CRD bounds it to 1..4094
		if static {
			vlan.VlanAddresses, vlan.VlanRoutes = n.Addresses, routes
		} else {
			vlan.VlanDHCP = new(true)
		}

		dev.DeviceVlans = mv1alpha1.VlanList{vlan}
	} else if static {
		dev.DeviceAddresses, dev.DeviceRoutes = n.Addresses, routes
	} else {
		dev.DeviceDHCP = new(true)
	}

	return mergeDocument(cfg, "network", &mv1alpha1.Config{
		ConfigVersion: mv1alpha1.Version,
		MachineConfig: &mv1alpha1.MachineConfig{
			MachineNetwork: &mv1alpha1.NetworkConfig{
				NetworkInterfaces: mv1alpha1.NetworkDeviceList{dev},
				NameServers:       n.DNSServers,
			},
		},
	})
}

// injectDisks renders spec.disks: a VolumeConfig for EPHEMERAL and a UserVolumeConfig
// per user volume, each selecting its disk with the CEL expression diskExpression builds.
func injectDisks(cfg coreconfig.Provider, m *ichorv1alpha1.TalosMachine) (coreconfig.Provider, error) {
	for _, d := range m.Spec.Disks {
		match, err := diskExpression(d.Disk)
		if err != nil {
			return nil, err
		}

		switch d.Role {
		case ichorv1alpha1.MachineDiskRoleEphemeral:
			doc := block.NewVolumeConfigV1Alpha1()
			doc.MetaName = ephemeralVolume
			doc.ProvisioningSpec.DiskSelectorSpec.Match = match

			cfg, err = mergeDocument(cfg, "ephemeral volume", doc)
		case ichorv1alpha1.MachineDiskRoleUserVolume:
			doc := block.NewUserVolumeConfigV1Alpha1()
			doc.MetaName = d.Name
			doc.ProvisioningSpec.DiskSelectorSpec.Match = match
			doc.ProvisioningSpec.ProvisioningGrow = new(true)

			// Talos wants a minimum or a maximum; an uncapped volume takes the whole disk.
			if d.MaxSize == "" {
				doc.ProvisioningSpec.ProvisioningMinSize = block.MustByteSize("1GiB")
			} else if err = doc.ProvisioningSpec.ProvisioningMaxSize.UnmarshalText([]byte(d.MaxSize)); err != nil {
				return nil, fmt.Errorf("render: disks %q maxSize: %w", d.Name, err)
			}

			cfg, err = mergeDocument(cfg, "user volume", doc)
		default:
			err = fmt.Errorf("render: unknown disk role %q", d.Role)
		}

		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}
