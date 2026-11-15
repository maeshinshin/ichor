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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	wantNeedsIP = "needs an IP address"
	wantRemoved = "endpoint type loadBalancer is removed"
)

func endpointCluster(typ, addr string) *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{
		Endpoint: ichorv1alpha1.TalosClusterEndpoint{Type: typ, Address: addr},
	}}
}

// A new cluster's endpoint is always checked; an update is checked only when it changes the type or the address,
// so a stored object with a mismatched pair stays editable in every other respect.
func TestValidateEndpoint(t *testing.T) {
	const host, ip = "cluster.example.com", "10.0.0.5"

	for name, tc := range map[string]struct {
		old, new *ichorv1alpha1.TalosCluster
		want     string
	}{
		"create: a hostname with vip is refused":          {nil, endpointCluster("vip", host), wantNeedsIP},
		"create: node takes a hostname":                   {nil, endpointCluster("node", host), ""},
		"create: node takes an IP":                        {nil, endpointCluster("node", ip), ""},
		"create: loadBalancer is refused for a name":      {nil, endpointCluster("loadBalancer", host), wantRemoved},
		"create: loadBalancer is refused for an IP":       {nil, endpointCluster("loadBalancer", ip), wantRemoved},
		"create: a vip with an IP":                        {nil, endpointCluster("vip", ip), ""},
		"update: another field of a mismatched pair":      {endpointCluster("vip", host), endpointCluster("vip", host), ""},
		"update: the address changes to a mismatch":       {endpointCluster("vip", ip), endpointCluster("vip", host), wantNeedsIP},
		"update: the type changes to a mismatch":          {endpointCluster("node", ip), endpointCluster("vip", host), wantNeedsIP},
		"update: a mismatched pair changes to a fit":      {endpointCluster("vip", host), endpointCluster("node", host), ""},
		"update: a mismatched pair changes to another":    {endpointCluster("vip", host), endpointCluster("vip", "other.example.com"), wantNeedsIP},
		"update: a stored loadBalancer is left alone":     {endpointCluster("loadBalancer", host), endpointCluster("loadBalancer", host), ""},
		"update: a stored loadBalancer moves to node":     {endpointCluster("loadBalancer", host), endpointCluster("node", host), ""},
		"update: a node endpoint changes to loadBalancer": {endpointCluster("node", ip), endpointCluster("loadBalancer", host), wantRemoved},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateEndpoint(tc.old, tc.new)
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
