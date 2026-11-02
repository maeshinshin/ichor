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
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/network"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/nethelpers"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Ports the firewall opens besides Talos's own (constants.*Port): Cilium's tunnel and health
// check (Cilium 1.18 defaults: vxlan 8472, geneve 6081, agent health 4240) and its WireGuard
// listen port (encryption-wireguard docs: UDP 51871).
const (
	portCiliumVXLAN     = 8472
	portCiliumGeneve    = 6081
	portCiliumHealth    = 4240
	portCiliumWireGuard = 51871

	// ruleNameFirewall prefixes every NetworkRuleConfig the firewall option renders.
	ruleNameFirewall = "ichor-fw-"
)

// FirewallEnabled reports spec.security.firewall.enabled.
func FirewallEnabled(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.Security != nil && c.Spec.Security.Firewall != nil && c.Spec.Security.Firewall.Enabled
}

// WireGuardEnabled reports spec.security.wireGuard.enabled.
func WireGuardEnabled(c *ichorv1alpha1.TalosCluster) bool {
	return c.Spec.Security != nil && c.Spec.Security.WireGuard != nil && c.Spec.Security.WireGuard.Enabled
}

// parsePrefixes parses CIDRs, masked to their network address; what names the field in an error.
func parsePrefixes(what string, cidrs []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(cidrs))

	for _, s := range cidrs {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", what, s, err)
		}

		out = append(out, p.Masked())
	}

	return out, nil
}

// ControllerSourceAllowed reports whether src, the address Ichor's controller reaches the
// cluster from, is inside spec.security.firewall.controllerCIDRs. It is the preflight that
// refuses a firewall which would cut Ichor's own access to apid and the Kubernetes API.
func ControllerSourceAllowed(c *ichorv1alpha1.TalosCluster, src netip.Addr) error {
	if !FirewallEnabled(c) {
		return nil
	}

	prefixes, err := parsePrefixes("spec.security.firewall.controllerCIDRs", c.Spec.Security.Firewall.ControllerCIDRs)
	if err != nil {
		return err
	}

	src = src.Unmap()

	for _, p := range prefixes {
		if p.Contains(src) {
			return nil
		}
	}

	return fmt.Errorf("spec.security.firewall.controllerCIDRs %v does not contain %s, the address Ichor's controller "+
		"connects from: the firewall would block Ichor from apid (50000) and the Kubernetes API (6443)",
		c.Spec.Security.Firewall.ControllerCIDRs, src)
}

// fwRule is one planned NetworkRuleConfig.
type fwRule struct {
	name    string
	proto   nethelpers.Protocol
	ports   network.PortRanges
	sources []netip.Prefix
}

func single(p uint16) network.PortRange { return network.PortRange{Lo: p, Hi: p} }

// parsePortRange reads "443" or "8000-8100".
func parsePortRange(s string) (network.PortRange, error) {
	lo, hi, isRange := strings.Cut(s, "-")

	l, err := strconv.ParseUint(lo, 10, 16)
	if err != nil || l == 0 {
		return network.PortRange{}, fmt.Errorf("port %q is not in 1-65535", s)
	}

	h := l

	if isRange {
		if h, err = strconv.ParseUint(hi, 10, 16); err != nil || h < l {
			return network.PortRange{}, fmt.Errorf("port range %q is not valid", s)
		}
	}

	return network.PortRange{Lo: uint16(l), Hi: uint16(h)}, nil
}

// concat joins prefix lists, dropping duplicates and keeping the first occurrence's order.
func concat(lists ...[]netip.Prefix) []netip.Prefix {
	var out []netip.Prefix

	seen := map[netip.Prefix]bool{}

	for _, l := range lists {
		for _, p := range l {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}

	return out
}

// firewallPlan computes the rules for one node of the cluster. It is pure and the single source
// of what the firewall opens.
//
//nolint:funlen // one flat list of the ports, easier to audit unsplit
func firewallPlan(c *ichorv1alpha1.TalosCluster, machineType machine.Type) ([]fwRule, error) {
	fw := c.Spec.Security.Firewall

	if len(fw.ControllerCIDRs) == 0 || len(fw.NodeCIDRs) == 0 {
		return nil, errors.New("render: firewall is enabled but controllerCIDRs or nodeCIDRs is empty: Ichor or the nodes would lose access")
	}

	var (
		controller, nodes, cps, talosAPI, apiServer []netip.Prefix
		err                                         error
	)

	for _, f := range []struct {
		dst   *[]netip.Prefix
		name  string
		cidrs []string
	}{
		{&controller, "controllerCIDRs", fw.ControllerCIDRs}, {&nodes, "nodeCIDRs", fw.NodeCIDRs},
		{&cps, "controlPlaneCIDRs", fw.ControlPlaneCIDRs}, {&talosAPI, "talosAPICIDRs", fw.TalosAPICIDRs},
		{&apiServer, "apiServerCIDRs", fw.APIServerCIDRs},
	} {
		if *f.dst, err = parsePrefixes("render: spec.security.firewall."+f.name, f.cidrs); err != nil {
			return nil, err
		}
	}

	if len(cps) == 0 {
		cps = nodes
	}

	pods, services := []string{constants.DefaultIPv4PodCIDR}, []string{constants.DefaultIPv4ServiceCIDR}

	if n := c.Spec.Network; n != nil {
		if len(n.PodSubnets) > 0 {
			pods = n.PodSubnets
		}

		if len(n.ServiceSubnets) > 0 {
			services = n.ServiceSubnets
		}
	}

	podNet, err := parsePrefixes("render: spec.network.podSubnets", pods)
	if err != nil {
		return nil, err
	}

	svcNet, err := parsePrefixes("render: spec.network.serviceSubnets", services)
	if err != nil {
		return nil, err
	}

	tunnel := uint16(portCiliumVXLAN)
	if c.Spec.CNI.Tunnel == "geneve" {
		tunnel = portCiliumGeneve
	}

	tcp, udp := nethelpers.ProtocolTCP, nethelpers.ProtocolUDP
	isCP := machineType == machine.TypeControlPlane

	rules := []fwRule{
		{"apid", tcp, network.PortRanges{single(constants.ApidPort)}, concat(controller, talosAPI, cps)},
		{"kubelet", tcp, network.PortRanges{single(constants.KubeletPort)}, concat(cps, podNet)},
		{"cilium-tunnel", udp, network.PortRanges{single(tunnel)}, nodes},
		{"cilium-health", tcp, network.PortRanges{single(portCiliumHealth)}, nodes},
	}

	if isCP {
		rules = append(rules,
			fwRule{"trustd", tcp, network.PortRanges{single(constants.TrustdPort)}, nodes},
			fwRule{"etcd", tcp, network.PortRanges{{Lo: constants.EtcdClientPort, Hi: constants.EtcdPeerPort}}, cps},
			fwRule{"kubernetes-api", tcp, network.PortRanges{single(constants.DefaultControlPlanePort)},
				concat(controller, apiServer, nodes, podNet, svcNet)},
		)
	}

	if WireGuardEnabled(c) {
		rules = append(rules, fwRule{"cilium-wireguard", udp, network.PortRanges{single(portCiliumWireGuard)}, nodes})
	}

	if fw.KubeSpan {
		rules = append(rules, fwRule{"kubespan", udp, network.PortRanges{single(constants.KubeSpanDefaultPort)},
			[]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}})
	}

	for _, a := range fw.Allow {
		r, err := allowRule(a)
		if err != nil {
			return nil, err
		}

		rules = append(rules, r)
	}

	return rules, nil
}

// allowRule converts one spec.security.firewall.allow entry.
func allowRule(a ichorv1alpha1.TalosClusterFirewallRule) (fwRule, error) {
	r := fwRule{name: "allow-" + a.Name, proto: nethelpers.ProtocolTCP}

	if a.Protocol == "udp" {
		r.proto = nethelpers.ProtocolUDP
	}

	for _, s := range a.Ports {
		pr, err := parsePortRange(s)
		if err != nil {
			return fwRule{}, fmt.Errorf("render: spec.security.firewall.allow[%s]: %w", a.Name, err)
		}

		r.ports = append(r.ports, pr)
	}

	if err := r.ports.Validate(); err != nil {
		return fwRule{}, fmt.Errorf("render: spec.security.firewall.allow[%s]: %w", a.Name, err)
	}

	var err error

	if r.sources, err = parsePrefixes("render: spec.security.firewall.allow["+a.Name+"].cidrs", a.CIDRs); err != nil {
		return fwRule{}, err
	}

	return r, nil
}

// injectFirewall renders spec.security.firewall: the default action block and one rule
// per service the cluster needs, on every node. It is a system-layer step; unset or disabled
// returns cfg untouched, so the render is byte-identical to before.
func injectFirewall(
	cfg coreconfig.Provider, cluster *ichorv1alpha1.TalosCluster, machineType machine.Type,
) (coreconfig.Provider, error) {
	if !FirewallEnabled(cluster) {
		return cfg, nil
	}

	rules, err := firewallPlan(cluster, machineType)
	if err != nil {
		return nil, err
	}

	def := network.NewDefaultActionConfigV1Alpha1()
	def.Ingress = nethelpers.DefaultActionBlock

	docs := []config.Document{def}

	for _, r := range rules {
		d := network.NewRuleConfigV1Alpha1()
		d.MetaName = ruleNameFirewall + r.name
		d.PortSelector.Protocol = r.proto
		d.PortSelector.Ports = r.ports

		for _, s := range r.sources {
			d.Ingress = append(d.Ingress, network.IngressRule{Subnet: s})
		}

		docs = append(docs, d)
	}

	return mergeDocument(cfg, "ingress firewall", docs...)
}
