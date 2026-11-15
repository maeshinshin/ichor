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
	"strings"
	"testing"
)

const (
	epHost = "cluster.example.com"
	epIP   = "10.0.0.1"
)

// nodeAddressRefusal is the refusal for a node address that is neither an IP address nor a host name.
const nodeAddressRefusal = "neither an IP address nor a host name"

func TestEndpointCheckAddress(t *testing.T) {
	long := strings.Repeat("a", 64) + ".example.com"

	for _, tc := range []struct {
		typ, addr, want string
	}{
		{EndpointTypeVIP, "192.168.0.210", ""},
		{EndpointTypeVIP, "fd00::210", ""},
		{EndpointTypeVIP, epHost, "needs an IP address; type node would take a hostname"},
		{EndpointTypeVIP, "192.168.1.300", "is not an IP address"},
		{EndpointTypeNode, epIP, ""},
		{EndpointTypeNode, "fd00::50", ""},
		{EndpointTypeNode, "kube.example.lan", ""},
		{EndpointTypeNode, "k8s-api1.example.com", ""},
		{EndpointTypeNode, "kube", ""},
		{EndpointTypeNode, "192.168.1.300", nodeAddressRefusal},
		{EndpointTypeNode, "10.0.0", nodeAddressRefusal},
		{EndpointTypeNode, "Kube.example.lan", nodeAddressRefusal},
		{EndpointTypeNode, "kube.example.lan.", nodeAddressRefusal},
		{EndpointTypeNode, "kube..example.lan", nodeAddressRefusal},
		{EndpointTypeNode, "-kube.example.lan", nodeAddressRefusal},
		{EndpointTypeNode, "kube_api.example.lan", nodeAddressRefusal},
		{EndpointTypeNode, "https://kube.example.lan", nodeAddressRefusal},
		{EndpointTypeNode, long, nodeAddressRefusal},
		{EndpointTypeLoadBalancer, epHost, "endpoint type loadBalancer is removed: use type node"},
		{EndpointTypeLoadBalancer, epIP, "endpoint type loadBalancer is removed: use type node"},
	} {
		err := TalosClusterEndpoint{Type: tc.typ, Address: tc.addr}.CheckAddress()
		if (tc.want == "") != (err == nil) {
			t.Errorf("%s %q: err = %v, want refused %v", tc.typ, tc.addr, err, tc.want != "")

			continue
		}

		if err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %q: err = %v, want one containing %q", tc.typ, tc.addr, err, tc.want)
		}
	}
}
