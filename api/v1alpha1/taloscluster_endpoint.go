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
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Endpoint types of TalosClusterEndpoint.Type. loadBalancer is deprecated and refused (ADR 0161): it stays in the
// enum so that a stored object still reads, and node takes a load balancer's address or name.
const (
	EndpointTypeVIP          = "vip"
	EndpointTypeLoadBalancer = "loadBalancer"
	EndpointTypeNode         = "node"
)

// ErrLoadBalancerRemoved is the refusal for the deprecated type. It names the type that replaces it.
var ErrLoadBalancerRemoved = errors.New("endpoint type loadBalancer is removed: " +
	"use type node, and give the load balancer's IP address or name as the address")

// CheckAddress reports whether the endpoint's address has the shape its type needs: vip takes an IP address (a VIP
// is also written into a Layer2VIPConfig, which only takes one), node takes an IP address or a host name, and
// loadBalancer is refused. The error says what was given, what the type needs, and which type would have taken it.
func (e TalosClusterEndpoint) CheckAddress() error {
	if e.Type == EndpointTypeLoadBalancer {
		return ErrLoadBalancerRemoved
	}

	if _, err := netip.ParseAddr(e.Address); err == nil {
		return nil
	}

	switch e.Type {
	case EndpointTypeVIP:
		if checkHostname(e.Address) == nil {
			return fmt.Errorf("endpoint address %q is a hostname, but endpoint type vip needs an IP address; "+
				"type %s would take a hostname", e.Address, EndpointTypeNode)
		}

		return fmt.Errorf("endpoint address %q is not an IP address, and endpoint type vip needs one", e.Address)
	case EndpointTypeNode:
		if err := checkHostname(e.Address); err != nil {
			return fmt.Errorf("endpoint address %q is neither an IP address nor a host name: %w", e.Address, err)
		}
	}

	return nil
}

// checkHostname refuses what is not a DNS host name: at most 253 characters, each dot-separated label a lower-case
// RFC 1123 label of at most 63. The last label may not be all digits, so a mistyped address such as 192.168.1.300
// is refused and not taken for a name.
func checkHostname(name string) error {
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return errors.New(errs[0])
	}

	labels := strings.Split(name, ".")
	for _, l := range labels {
		if errs := validation.IsDNS1123Label(l); len(errs) > 0 {
			return errors.New(errs[0])
		}
	}

	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return errors.New("its last label is all digits, which is how a mistyped IP address looks")
	}

	return nil
}

// CheckVIPHeldBy refuses a vip endpoint that control plane m could never hold: with static addresses, the VIP must
// lie inside one of their subnets (a Layer2 VIP is a second address on the same link, so an address elsewhere is a
// load balancer's, or a typo) and must not be one of the node's own addresses. A DHCP machine names no subnet, so
// it is not checked. The renderer and the TalosCluster webhook both call it.
func (e TalosClusterEndpoint) CheckVIPHeldBy(m *TalosMachine) error {
	n := m.Spec.Network
	if e.Type != EndpointTypeVIP || m.Spec.Role != "controlplane" || n == nil ||
		n.Addressing != MachineAddressingStatic {
		return nil
	}

	vip, err := netip.ParseAddr(e.Address)
	if err != nil {
		return nil // CheckAddress reports it
	}

	inside := false

	for _, a := range n.Addresses {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			continue
		}

		if p.Addr() == vip {
			return fmt.Errorf("spec.endpoint.address %s is control plane %s's own address in spec.network.addresses: "+
				"give the VIP an address no node uses", vip, m.Name)
		}

		inside = inside || p.Contains(vip)
	}

	if !inside {
		return fmt.Errorf("spec.endpoint.address %s is outside every subnet of control plane %s (spec.network.addresses %v), "+
			"so no node could hold it as a VIP: if a load balancer holds it, set spec.endpoint.type to %s; "+
			"otherwise choose a free address inside the nodes' subnet",
			vip, m.Name, n.Addresses, EndpointTypeNode)
	}

	return nil
}
