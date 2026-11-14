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
	"encoding/json"
	"fmt"
	"net"
	"net/netip"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// The Cilium features `ichor init` turns on. Both need Cilium's kube-proxy replacement, which is the
// --kube-proxy replaced mode. Init only: an existing cluster is not changed by either flag in this release.

// kubeProxyMode is the --kube-proxy mode; "" (the flag not given) is enabled, Talos's own kube-proxy.
func (in initPlanInput) kubeProxyMode() cni.KubeProxyMode {
	if in.KubeProxy == "" {
		return cni.KubeProxyEnabled
	}

	return cni.KubeProxyMode(in.KubeProxy)
}

// checkCiliumFeatures refuses a --kube-proxy value that is neither mode, and the --gateway-api and
// --l2-announcements combinations that cannot work, before anything is written.
func checkCiliumFeatures(in initPlanInput, nodeEndpoint string) error {
	mode := in.kubeProxyMode()
	if err := mode.Validate(); err != nil {
		return fmt.Errorf("init: --kube-proxy %q is invalid: must be %q or %q", in.KubeProxy, cni.KubeProxyEnabled, cni.KubeProxyReplaced)
	}

	if in.L2Pool != "" && !in.L2Announcements {
		return fmt.Errorf("init: --l2-pool is only used with --l2-announcements")
	}

	if in.GatewayAPI && mode != cni.KubeProxyReplaced {
		return fmt.Errorf("init: --gateway-api needs kube-proxy replacement: pass --kube-proxy replaced (spec.cni.kubeProxy=replaced); " +
			"Cilium's Gateway API requires kubeProxyReplacement=true")
	}

	if !in.L2Announcements {
		return nil
	}

	if mode != cni.KubeProxyReplaced {
		return fmt.Errorf("init: --l2-announcements needs kube-proxy replacement: pass --kube-proxy replaced (spec.cni.kubeProxy=replaced); " +
			"Cilium's L2 announcements require kubeProxyReplacement=true")
	}

	if in.L2Pool == "" {
		return fmt.Errorf("init: --l2-announcements needs --l2-pool, the CIDR that LoadBalancer addresses come from")
	}

	return checkL2Pool(in, nodeEndpoint)
}

// ownedAddress is an address or network the pool must not overlap, and what to call it in the refusal.
type ownedAddress struct {
	what   string
	prefix netip.Prefix
}

// checkL2Pool refuses a pool that is not an IPv4 network in CIDR form, that overlaps an address Ichor or the
// node already owns (two ARP owners for one address is a fault nobody diagnoses quickly), or that is outside
// the first node's prefix. The overlap check runs first, so a pool that covers an address names that address.
func checkL2Pool(in initPlanInput, nodeEndpoint string) error {
	pool, err := netip.ParsePrefix(in.L2Pool)
	if err != nil || !pool.Addr().Is4() || pool.Masked() != pool {
		return fmt.Errorf("init: --l2-pool %q must be an IPv4 network in CIDR form, like 192.168.1.200/29", in.L2Pool)
	}

	var owned []ownedAddress

	addAddr := func(what, addr string) {
		if a, err := netip.ParseAddr(addr); err == nil {
			owned = append(owned, ownedAddress{what, netip.PrefixFrom(a, a.BitLen())})
		}
	}

	addAddr("the cluster endpoint address (--endpoint-address)", in.EndpointAddress)

	if host, _, err := net.SplitHostPort(nodeEndpoint); err == nil {
		addAddr("the first node's address (--node-endpoint)", host)
	}

	for _, a := range in.Addresses {
		if p, err := netip.ParsePrefix(a); err == nil {
			owned = append(owned, ownedAddress{"the first node's address (--address " + a + ")", netip.PrefixFrom(p.Addr(), p.Addr().BitLen())})
		}
	}

	for _, a := range in.DiscoverAddresses {
		addAddr("the discovery address (--discover-address)", a)
	}

	for _, c := range in.DiscoverCIDRs {
		if p, err := netip.ParsePrefix(c); err == nil {
			owned = append(owned, ownedAddress{"the discovery CIDR (--discover-cidr " + c + ")", p.Masked()})
		}
	}

	for _, o := range owned {
		if o.prefix.Contains(pool.Addr()) || pool.Contains(o.prefix.Addr()) {
			return fmt.Errorf("init: --l2-pool %s overlaps %s %s: two owners would announce the same address",
				in.L2Pool, o.what, o.prefix.Addr())
		}
	}

	if len(in.Addresses) == 0 {
		return fmt.Errorf("init: --l2-pool %s cannot be checked against the first node's prefix: "+
			"give the node's static address with --address (with DHCP the prefix is not known)", in.L2Pool)
	}

	node, err := netip.ParsePrefix(in.Addresses[0])
	if err != nil {
		return fmt.Errorf("init: --address %q: %w", in.Addresses[0], err)
	}

	subnet := node.Masked()
	if subnet.Bits() > pool.Bits() || !subnet.Contains(pool.Addr()) {
		return fmt.Errorf("init: --l2-pool %s is outside the first node's prefix %s (from --address %s)",
			in.L2Pool, subnet, in.Addresses[0])
	}

	return nil
}

// helmEnabledKey is the chart's enabled key under each feature block.
const helmEnabledKey = "enabled"

// ciliumUserValues is the spec.cni.cilium.values layer for the features the flags turn on, nil when none is.
func ciliumUserValues(in initPlanInput) (*ichorv1alpha1.TalosClusterCNICilium, error) {
	values := map[string]any{}
	if in.GatewayAPI {
		values["gatewayAPI"] = map[string]any{helmEnabledKey: true}
	}

	if in.L2Announcements {
		values["l2announcements"] = map[string]any{helmEnabledKey: true}
	}

	if len(values) == 0 {
		return nil, nil
	}

	raw, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("init: encoding the Cilium values: %w", err)
	}

	return &ichorv1alpha1.TalosClusterCNICilium{Values: &apiextensionsv1.JSON{Raw: raw}}, nil
}
