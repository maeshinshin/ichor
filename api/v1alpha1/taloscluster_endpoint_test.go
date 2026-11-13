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

func TestEndpointCheckAddress(t *testing.T) {
	for _, tc := range []struct {
		typ, addr, want string
	}{
		{EndpointTypeVIP, "192.168.0.210", ""},
		{EndpointTypeVIP, "fd00::210", ""},
		{EndpointTypeVIP, epHost, "needs an IP address; type loadBalancer would take a hostname"},
		{EndpointTypeNode, epIP, ""},
		{EndpointTypeNode, "node1.example.com", "endpoint type node needs an IP address"},
		{EndpointTypeLoadBalancer, epHost, ""},
		{EndpointTypeLoadBalancer, epIP, ""},
	} {
		err := TalosClusterEndpoint{Type: tc.typ, Address: tc.addr}.CheckAddress()
		if (tc.want == "") != (err == nil) || (err != nil && (!strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.addr))) {
			t.Errorf("%s %q: err = %v, want one containing %q", tc.typ, tc.addr, err, tc.want)
		}
	}
}
