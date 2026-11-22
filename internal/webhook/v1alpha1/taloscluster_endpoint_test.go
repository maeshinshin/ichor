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
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

// Switching an endpoint to vip warns about each approved control plane that has no spec.network: the renderer puts the
// VIP on a link, so such a node never holds it.
func TestVIPEndpointWarnsOfControlPlanesWithoutANetwork(t *testing.T) {
	const ns, cluster = "default", "c"

	node := func(name, role string, n *ichorv1alpha1.TalosMachineNetwork) *ichorv1alpha1.TalosMachine {
		m := &ichorv1alpha1.TalosMachine{Name: name, Namespace: ns}
		m.Spec.ClusterRef.Name, m.Spec.Role, m.Spec.Network = cluster, role, n
		m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved

		return m
	}

	reader := fake.NewClientBuilder().WithScheme(schemeOf(t)).WithObjects(
		node("talos02", roleControlPlane, nil),
		node("talos01", roleControlPlane, &ichorv1alpha1.TalosMachineNetwork{MAC: testMAC1}),
		node("worker", "worker", nil),
	).Build()
	v := &TalosClusterCustomValidator{APIReader: reader}

	update := func(oldObj, newObj *ichorv1alpha1.TalosCluster) []string {
		oldObj.Namespace, oldObj.Name, newObj.Namespace, newObj.Name = ns, cluster, ns, cluster

		warns, err := v.ValidateUpdate(context.Background(), oldObj, newObj)
		if err != nil {
			t.Fatal(err)
		}

		return warns
	}

	warns := update(endpointCluster("node", "192.168.0.202"), endpointCluster("vip", "192.168.0.210"))
	if len(warns) != 1 || !strings.Contains(warns[0], "talos02") || strings.Contains(warns[0], "talos01") ||
		strings.Contains(warns[0], "worker") {
		t.Fatalf("warnings = %q, want one naming only talos02", warns)
	}

	if warns := update(endpointCluster("vip", "192.168.0.210"), endpointCluster("vip", "192.168.0.210")); len(warns) != 0 {
		t.Fatalf("an update that leaves the endpoint alone warned: %q", warns)
	}

	if warns := update(endpointCluster("vip", "192.168.0.210"), endpointCluster("node", "192.168.0.202")); len(warns) != 0 {
		t.Fatalf("a node endpoint warned: %q", warns)
	}
}

// A vip endpoint no approved control plane with static addresses could hold is refused at admission, with the
// same message the renderer gives; one inside the subnet, or an unchanged endpoint, is admitted.
func TestVIPEndpointRefusedWhenNoControlPlaneCouldHoldIt(t *testing.T) {
	const ns, cluster = "default", "c"

	m := &ichorv1alpha1.TalosMachine{Name: "talos01", Namespace: ns}
	m.Spec.ClusterRef.Name, m.Spec.Role = cluster, roleControlPlane
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
	m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{
		MAC: testMAC1, Addressing: ichorv1alpha1.MachineAddressingStatic, Addresses: []string{"192.168.0.201/24"},
	}
	v := &TalosClusterCustomValidator{APIReader: fake.NewClientBuilder().WithScheme(schemeOf(t)).WithObjects(m).Build()}

	update := func(oldObj, newObj *ichorv1alpha1.TalosCluster) error {
		oldObj.Namespace, oldObj.Name, newObj.Namespace, newObj.Name = ns, cluster, ns, cluster
		_, err := v.ValidateUpdate(context.Background(), oldObj, newObj)

		return err
	}

	err := update(endpointCluster("node", "10.9.9.9"), endpointCluster("vip", "10.9.9.9"))
	if err == nil || !strings.Contains(err.Error(), "spec.endpoint.address 10.9.9.9 is outside every subnet") {
		t.Fatalf("error = %v, want the outside-every-subnet refusal", err)
	}

	if err := update(endpointCluster("node", "10.9.9.9"), endpointCluster("vip", "192.168.0.210")); err != nil {
		t.Fatalf("a VIP inside the subnet was refused: %v", err)
	}

	if err := update(endpointCluster("vip", "10.9.9.9"), endpointCluster("vip", "10.9.9.9")); err != nil {
		t.Fatalf("an unchanged endpoint was refused: %v", err)
	}
}

const testMAC1 = "aa:bb:cc:dd:ee:01"
