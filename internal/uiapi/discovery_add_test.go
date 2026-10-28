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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const knownAddr = "10.0.0.55"

func discoveryCluster(d *ichorv1alpha1.TalosClusterDiscovery) *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		Name: "c", Namespace: testNS,
		Spec: ichorv1alpha1.TalosClusterSpec{Discovery: d},
	}
}

func addAddrReq(addr string) *connect.Request[pb.AddDiscoveryAddressRequest] {
	return connect.NewRequest(&pb.AddDiscoveryAddressRequest{
		Cluster: objectRef(testNS, "c"), Address: addr, Reason: "new rack",
	})
}

func TestAddDiscoveryAddressAppendsOnlyThatAddress(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}, CIDRs: []string{"10.1.0.0/24"}})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}

	resp, err := s.AddDiscoveryAddress(context.Background(), addAddrReq(" 10.0.0.56 "))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Msg.GetAudit().GetAction() != "add-discovery-address" {
		t.Fatalf("audit action %q", resp.Msg.GetAudit().GetAction())
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	d := got.Spec.Discovery
	if d == nil || len(d.Addresses) != 2 || d.Addresses[1] != "10.0.0.56" || len(d.CIDRs) != 1 {
		t.Fatalf("want one address appended and the CIDRs kept, got %+v", d)
	}

	if len(resp.Msg.GetCluster().GetDiscoveryAddresses()) != 2 {
		t.Fatalf("the response should carry the stored cluster, got %v", resp.Msg.GetCluster())
	}
}

func TestAddDiscoveryAddressCreatesTheDiscoveryBlock(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(discoveryCluster(nil)).Build()

	if _, err := (&Server{Client: k8s}).AddDiscoveryAddress(context.Background(), addAddrReq("192.168.0.50")); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.Discovery == nil || len(got.Spec.Discovery.Addresses) != 1 {
		t.Fatalf("got %+v", got.Spec.Discovery)
	}
}

func TestAddDiscoveryAddressRefusesBadInput(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}

	for _, bad := range []string{"", "not-an-ip", "10.0.0.0/24", "fe80::1", knownAddr, "300.1.1.1"} {
		_, err := s.AddDiscoveryAddress(context.Background(), addAddrReq(bad))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%q: want InvalidArgument, got %v", bad, err)
		}
	}
}
