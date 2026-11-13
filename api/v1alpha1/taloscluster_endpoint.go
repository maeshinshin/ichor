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
	"fmt"
	"net/netip"
)

// Endpoint types of TalosClusterEndpoint.Type.
const (
	EndpointTypeVIP          = "vip"
	EndpointTypeLoadBalancer = "loadBalancer"
	EndpointTypeDNS          = "dns"
	EndpointTypeNode         = "node"
)

// CheckAddress reports whether the endpoint's address has the shape its type needs: vip and node take an IP address
// (a VIP is also written into a Layer2VIPConfig, which only takes one), dns takes a name, and loadBalancer takes
// either. The error says what was given, what the type needs, and which types would have accepted it.
func (e TalosClusterEndpoint) CheckAddress() error {
	_, err := netip.ParseAddr(e.Address)
	isIP := err == nil

	switch {
	case (e.Type == EndpointTypeVIP || e.Type == EndpointTypeNode) && !isIP:
		return fmt.Errorf("endpoint address %q is a hostname, but endpoint type %s needs an IP address; "+
			"type %s or %s would take a hostname", e.Address, e.Type, EndpointTypeLoadBalancer, EndpointTypeDNS)
	case e.Type == EndpointTypeDNS && isIP:
		return fmt.Errorf("endpoint address %q is an IP address, but endpoint type %s needs a hostname; "+
			"type %s, %s or %s would take an IP address", e.Address, e.Type, EndpointTypeVIP, EndpointTypeNode, EndpointTypeLoadBalancer)
	}

	return nil
}
