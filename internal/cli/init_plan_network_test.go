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
	"io"
	"testing"
)

func TestBuildInitPlan_NetworkSubnets(t *testing.T) {
	in := validInitPlanInput()
	in.PodSubnets = []string{"10.248.0.0/16"}
	in.ServiceSubnets = []string{"10.249.0.0/16"}

	cluster, err := buildInitPlan(in, io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	n := cluster.Spec.Network
	if n == nil || len(n.PodSubnets) != 1 || n.PodSubnets[0] != "10.248.0.0/16" ||
		len(n.ServiceSubnets) != 1 || n.ServiceSubnets[0] != "10.249.0.0/16" {
		t.Fatalf("spec.network = %+v, want the two subnets", n)
	}
}

func TestBuildInitPlan_NoSubnetsLeavesNetworkUnset(t *testing.T) {
	cluster, err := buildInitPlan(validInitPlanInput(), io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Spec.Network != nil {
		t.Fatalf("spec.network = %+v, want nil so Talos's defaults apply", cluster.Spec.Network)
	}
}

func TestBuildInitPlan_RejectsBadSubnets(t *testing.T) {
	for name, mutate := range map[string]func(*initPlanInput){
		"not a CIDR":  func(in *initPlanInput) { in.PodSubnets = []string{"10.248.0.0"} },
		"too many":    func(in *initPlanInput) { in.ServiceSubnets = []string{"10.1.0.0/16", "fd00::/64", "10.2.0.0/16"} },
		"bad service": func(in *initPlanInput) { in.ServiceSubnets = []string{"nope"} },
	} {
		t.Run(name, func(t *testing.T) {
			in := validInitPlanInput()
			mutate(&in)

			if _, err := buildInitPlan(in, io.Discard); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBuildInitPlan_Discovery(t *testing.T) {
	in := validInitPlanInput()
	in.DiscoverAddresses = []string{"192.168.1.20", "192.168.1.21"}
	in.DiscoverCIDRs = []string{"192.168.2.0/24"}

	cluster, err := buildInitPlan(in, io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	d := cluster.Spec.Discovery
	if d == nil || len(d.Addresses) != 2 || d.Addresses[1] != "192.168.1.21" || len(d.CIDRs) != 1 || d.CIDRs[0] != "192.168.2.0/24" {
		t.Fatalf("spec.discovery = %+v, want both addresses and the CIDR", d)
	}
}

func TestBuildInitPlan_NoDiscoveryLeavesItUnset(t *testing.T) {
	cluster, err := buildInitPlan(validInitPlanInput(), io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Spec.Discovery != nil {
		t.Fatalf("spec.discovery = %+v, want nil so discovery stays off", cluster.Spec.Discovery)
	}
}

func TestBuildInitPlan_RejectsBadDiscovery(t *testing.T) {
	for name, mutate := range map[string]func(*initPlanInput){
		"not an address": func(in *initPlanInput) { in.DiscoverAddresses = []string{"nope"} },
		"ipv6 address":   func(in *initPlanInput) { in.DiscoverAddresses = []string{"fd00::1"} },
		"not a CIDR":     func(in *initPlanInput) { in.DiscoverCIDRs = []string{"192.168.2.0"} },
		"ipv6 CIDR":      func(in *initPlanInput) { in.DiscoverCIDRs = []string{"fd00::/120"} },
		"wider than /24": func(in *initPlanInput) { in.DiscoverCIDRs = []string{"10.0.0.0/16"} },
		"too many CIDRs": func(in *initPlanInput) {
			in.DiscoverCIDRs = []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := validInitPlanInput()
			mutate(&in)

			if _, err := buildInitPlan(in, io.Discard); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBuildInitPlan_RegistryMirrors(t *testing.T) {
	in := validInitPlanInput()
	in.RegistryMirrors = []string{"ghcr.io=http://10.0.0.5:5000"}

	cluster, err := buildInitPlan(in, io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	r := cluster.Spec.Registries
	if len(r) != 1 || r[0].Host != "ghcr.io" || len(r[0].Endpoints) != 1 || r[0].Endpoints[0] != "http://10.0.0.5:5000" ||
		r[0].AuthSecretRef != nil {
		t.Fatalf("spec.registries = %+v, want one anonymous ghcr.io mirror", r)
	}

	in.RegistryMirrors = []string{"no-equals-sign"}
	if _, err := buildInitPlan(in, io.Discard); err == nil {
		t.Fatal("expected an error for a mirror without HOST=URL")
	}
}
