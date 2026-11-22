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
	"testing"

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func setDeviceOwnership(t *testing.T, s *Server, on bool) (*connect.Response[pb.SetContainerdDeviceOwnershipResponse], error) {
	t.Helper()

	return s.SetContainerdDeviceOwnership(context.Background(), connect.NewRequest(
		&pb.SetContainerdDeviceOwnershipRequest{Cluster: objectRef(testNS, "c"), Enabled: on}))
}

func TestSetContainerdDeviceOwnershipWritesOnlyThatField(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}})
	c.Spec.VersionContract = "v1.14"
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}

	resp, err := setDeviceOwnership(t, s, true)
	if err != nil {
		t.Fatal(err)
	}

	if a := resp.Msg.GetAudit(); a.GetAction() != "set-containerd-device-ownership" {
		t.Fatalf("audit %v", a)
	}

	if !resp.Msg.GetCluster().GetContainerdDeviceOwnership() {
		t.Fatal("the response should carry the stored value")
	}

	if d := storedDiscovery(t, k8s); len(d.Addresses) != 1 {
		t.Fatalf("discovery changed: %+v", d)
	}

	if _, err := setDeviceOwnership(t, s, false); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), namespacedName(objectRef(testNS, "c")), &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.Containerd == nil || got.Spec.Containerd.DeviceOwnershipFromSecurityContext {
		t.Fatalf("want the field stored false, got %+v", got.Spec.Containerd)
	}
}

func TestSetContainerdDeviceOwnershipRefusesAnOldContract(t *testing.T) {
	c := discoveryCluster(nil)
	c.Spec.VersionContract = caLegacyContract
	s := &Server{Client: fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()}

	if _, err := setDeviceOwnership(t, s, true); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}
