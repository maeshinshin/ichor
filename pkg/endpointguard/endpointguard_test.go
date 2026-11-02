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

package endpointguard

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func clusterWith(
	d *ichorv1alpha1.TalosClusterDiscovery, n *ichorv1alpha1.TalosClusterNetwork,
) *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{Discovery: d, Network: n}}
}

func TestForbidden(t *testing.T) {
	net := &ichorv1alpha1.TalosClusterNetwork{
		PodSubnets: []string{"10.248.0.0/16"}, ServiceSubnets: []string{"10.249.0.0/16"},
	}
	c := clusterWith(nil, net)

	for _, tc := range []struct {
		name, endpoint string
		refused        bool
	}{
		{"plain ip", "192.168.10.5", false},
		{"ip with port", "192.168.10.5:50000", false},
		{"ipv6 with port", "[2001:db8::5]:50000", false},
		{"loopback", "127.0.0.1:50000", true},
		{"loopback other", "127.5.5.5", true},
		{"ipv6 loopback", "[::1]:50000", true},
		{"unspecified", "0.0.0.0:50000", true},
		{"ipv6 unspecified", "[::]:50000", true},
		{"link local metadata", "169.254.169.254:80", true},
		{"ipv6 link local", "[fe80::1]:50000", true},
		{"multicast", "224.0.0.1:50000", true},
		{"ipv6 multicast", "[ff02::1]:50000", true},
		{"v4 mapped loopback", "[::ffff:127.0.0.1]:50000", true},
		{"cluster pod range", "10.248.3.4:50000", true},
		{"cluster service range", "10.249.0.1:50000", true},
		{"outside the declared ranges", "10.244.3.4:50000", false},
		{"empty", "", true},
		{"empty host", ":50000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Forbidden(tc.endpoint, c)
			if (err != nil) != tc.refused {
				t.Fatalf("Forbidden(%q) = %v, refused want %v", tc.endpoint, err, tc.refused)
			}
		})
	}
}

func TestInDiscovery(t *testing.T) {
	c := clusterWith(&ichorv1alpha1.TalosClusterDiscovery{
		Addresses: []string{"192.168.1.9"}, CIDRs: []string{"10.0.5.0/24"},
	}, nil)

	for endpoint, want := range map[string]bool{
		"192.168.1.9:50000": true,
		"192.168.1.9":       true,
		"192.168.1.10":      false,
		"10.0.5.77:50000":   true,
		"10.0.6.1:50000":    false,
		"attacker.example":  false,
		"[::1]:50000":       false,
	} {
		if got := InDiscovery(endpoint, c); got != want {
			t.Errorf("InDiscovery(%q) = %v, want %v", endpoint, got, want)
		}
	}

	if InDiscovery("10.0.5.1", clusterWith(nil, nil)) {
		t.Error("a cluster without discovery names no address")
	}
}
