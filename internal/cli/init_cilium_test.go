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
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// lanInput is a first node on a /24 with the VIP, the node, the discovery addresses and a discovery CIDR all
// outside the pool the cases use (192.168.1.240/29), so each case moves one thing at a time.
func lanInput() initPlanInput {
	return initPlanInput{
		EndpointAddress:   "192.168.1.210",
		Addresses:         []string{"192.168.1.201/24"},
		DiscoverAddresses: []string{"192.168.1.203"},
		DiscoverCIDRs:     []string{"192.168.1.64/29"},
		GatewayAPI:        false,
		L2Announcements:   true,
		L2Pool:            "192.168.1.240/29",
	}
}

const lanNodeEndpoint = "192.168.1.250:50000"

// The refusal names the address it hit, checked as a substring.
const (
	wantEndpointAddress = "--endpoint-address"
	wantNodeEndpoint    = "--node-endpoint"
)

func TestCheckCiliumFeatures(t *testing.T) {
	cases := []struct {
		name     string
		mode     cni.KubeProxyMode
		in       func(*initPlanInput)
		node     string
		wantSubs string // "" means the combination is accepted
	}{
		{name: "nothing set passes with kube-proxy enabled", mode: cni.KubeProxyEnabled,
			in: func(in *initPlanInput) { in.L2Announcements, in.L2Pool = false, "" }},
		{name: "gateway-api refused with kube-proxy enabled", mode: cni.KubeProxyEnabled,
			in: func(in *initPlanInput) { in.L2Announcements, in.L2Pool, in.GatewayAPI = false, "", true }, wantSubs: "--gateway-api needs kube-proxy replacement"},
		{name: "gateway-api accepted with kube-proxy replaced", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Announcements, in.L2Pool, in.GatewayAPI = false, "", true }},
		{name: "l2-announcements refused with kube-proxy enabled", mode: cni.KubeProxyEnabled,
			wantSubs: "--l2-announcements needs kube-proxy replacement"},
		{name: "l2-announcements without a pool refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "" }, wantSubs: "--l2-announcements needs --l2-pool"},
		{name: "l2-pool without l2-announcements refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Announcements = false }, wantSubs: "--l2-pool is only used with --l2-announcements"},
		{name: "pool not in CIDR form refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.1.240" }, wantSubs: "must be an IPv4 network in CIDR form"},
		{name: "pool with host bits set refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.1.241/29" }, wantSubs: "in CIDR form"},
		{name: "pool covering the endpoint address refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.1.208/28" }, wantSubs: wantEndpointAddress},
		{name: "pool covering a node endpoint refused", mode: cni.KubeProxyReplaced,
			node: "192.168.1.244:50000", wantSubs: wantNodeEndpoint},
		{name: "pool covering a node address (--address) refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.1.200/29" }, node: "192.168.1.250:50000", wantSubs: "--address 192.168.1.201/24"},
		{name: "pool covering a discovery address refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) {
				in.L2Pool = "192.168.1.192/29"
				in.DiscoverAddresses = []string{"192.168.1.195"}
			},
			wantSubs: "--discover-address"},
		{name: "pool covering a discovery CIDR refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.1.64/28" }, wantSubs: "--discover-cidr 192.168.1.64/29"},
		{name: "a /16 pool is refused and names what it covers", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.L2Pool = "192.168.0.0/16" }, wantSubs: wantEndpointAddress},
		{name: "pool outside the first node's prefix refused", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) {
				in.EndpointAddress, in.Addresses, in.DiscoverAddresses, in.DiscoverCIDRs = flowCPAddr, []string{"10.0.0.5/24"}, nil, nil
				in.L2Pool = "192.168.1.240/29"
			}, node: "10.0.0.11:50000", wantSubs: "outside the first node's prefix 10.0.0.0/24"},
		{name: "pool with no --address refused (prefix unknown under DHCP)", mode: cni.KubeProxyReplaced,
			in: func(in *initPlanInput) { in.Addresses = nil }, wantSubs: "give the node's static address with --address"},
		{name: "a pool clear of everything passes", mode: cni.KubeProxyReplaced},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := lanInput()
			if tc.in != nil {
				tc.in(&in)
			}

			node := tc.node
			if node == "" {
				node = lanNodeEndpoint
			}

			in.KubeProxy = string(tc.mode)
			err := checkCiliumFeatures(in, node)
			switch {
			case tc.wantSubs == "" && err != nil:
				t.Fatalf("want accepted, got %v", err)
			case tc.wantSubs != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSubs)):
				t.Fatalf("want refusal containing %q, got %v", tc.wantSubs, err)
			}
		})
	}
}

func TestCiliumUserValues(t *testing.T) {
	v, err := ciliumUserValues(initPlanInput{GatewayAPI: true, L2Announcements: true})
	if err != nil || v == nil {
		t.Fatalf("ciliumUserValues: %v, %v", v, err)
	}

	var got map[string]map[string]bool
	if err := json.Unmarshal(v.Values.Raw, &got); err != nil {
		t.Fatal(err)
	}

	if !got["gatewayAPI"]["enabled"] || !got["l2announcements"]["enabled"] {
		t.Fatalf("want gatewayAPI.enabled and l2announcements.enabled, got %s", v.Values.Raw)
	}

	if none, _ := ciliumUserValues(initPlanInput{}); none != nil {
		t.Fatalf("no feature flag must leave the Cilium values unset, got %s", none.Values.Raw)
	}
}
