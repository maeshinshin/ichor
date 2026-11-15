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
