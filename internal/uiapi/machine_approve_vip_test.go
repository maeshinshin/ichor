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

package uiapi

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	vipTestAddr = "10.0.0.5"
	vipTestLink = "eno1"
)

// vipCluster is cluster "c" of the fixture machines with the given endpoint type.
func vipCluster(endpointType string) *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: testNS,
		Spec: ichorv1alpha1.TalosClusterSpec{Endpoint: ichorv1alpha1.TalosClusterEndpoint{Type: endpointType, Address: vipTestAddr}},
	}
}

// firstControlPlane is an approved control plane with a primary link: what makes the cluster's VIP Ichor's.
func firstControlPlane() *ichorv1alpha1.TalosMachine {
	m := testMachine()
	m.Name = nodeTestCP
	m.Spec.Role = roleControlPlane
	m.Spec.ApprovalState = ichorv1alpha1.MachineApprovalStateApproved
	m.Spec.Network = &ichorv1alpha1.TalosMachineNetwork{MAC: "aa:bb:cc:00:00:01", Addressing: ichorv1alpha1.MachineAddressingDHCP}

	return m
}

// A control plane approved without a primary link into a cluster whose VIP Ichor holds never joins the VIP
// election: the first node keeps the address for good. The approval is refused, and says what to pass.
func TestApproveControlPlaneWithoutLinkIntoVIPClusterIsRefused(t *testing.T) {
	pending := testMachine()
	pending.Status.DiscoveredLinks = []ichorv1alpha1.TalosMachineDiscoveredLink{
		{Name: vipTestLink, PermanentMAC: testMAC, LinkUp: true},
	}

	approve := func(t *testing.T, c client.Client, role pb.MachineRole, nic string) error {
		t.Helper()

		var network *pb.MachineNetwork
		if nic != "" {
			network = &pb.MachineNetwork{Mac: nic, Addressing: ichorv1alpha1.MachineAddressingDHCP}
		}

		s := &Server{Client: c}
		_, err := s.ApproveMachine(context.Background(), connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(testNS, "m1"), Role: role, Network: network,
		}))

		return err
	}

	build := func(objs ...client.Object) client.Client {
		return fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).WithStatusSubresource(pending).Build()
	}

	t.Run("refused, naming the flag and the machine's links", func(t *testing.T) {
		err := approve(t, build(pending.DeepCopy(), vipCluster("vip"), firstControlPlane()), pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, "")
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("want FailedPrecondition, got %v", err)
		}

		for _, want := range []string{"--nic", testMAC, vipTestLink} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q missing from %q", want, err)
			}
		}
	})

	t.Run("with a link it is approved", func(t *testing.T) {
		if err := approve(t, build(pending.DeepCopy(), vipCluster("vip"), firstControlPlane()),
			pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, testMAC); err != nil {
			t.Fatalf("approve with a link: %v", err)
		}
	})

	for _, tc := range []struct {
		name string
		role pb.MachineRole
		objs []client.Object
	}{
		{"a worker needs none", pb.MachineRole_MACHINE_ROLE_WORKER, []client.Object{vipCluster("vip"), firstControlPlane()}},
		{"another endpoint type has no VIP of Ichor's", pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE,
			[]client.Object{vipCluster("loadBalancer"), firstControlPlane()}},
		{"a vip no control plane has a link for is not Ichor's", pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE,
			[]client.Object{vipCluster("vip")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := approve(t, build(append(tc.objs, pending.DeepCopy())...), tc.role, ""); err != nil {
				t.Fatalf("approve: %v", err)
			}
		})
	}
}
