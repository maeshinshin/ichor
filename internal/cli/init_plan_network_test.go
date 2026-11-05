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

func TestBuildInitPlan_NoSubnetsNoDiscoveryLeavesThemUnset(t *testing.T) {
	cluster, err := buildInitPlan(validInitPlanInput(), io.Discard)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Spec.Network != nil || cluster.Spec.Discovery != nil {
		t.Fatalf("spec.network = %+v, spec.discovery = %+v, want nil so Talos's defaults apply and discovery stays off",
			cluster.Spec.Network, cluster.Spec.Discovery)
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
