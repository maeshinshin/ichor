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
	corev1 "k8s.io/api/core/v1"
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
		Cluster: objectRef(testNS, "c"), Address: addr,
	})
}

func TestAddDiscoveryAddressAppendsOnlyThatAddress(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}, CIDRs: []string{cidrA}})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}

	resp, err := s.AddDiscoveryAddress(context.Background(), addAddrReq(" 10.0.0.56 "))
	if err != nil {
		t.Fatal(err)
	}

	if resp.Msg.GetAudit().GetAction() != "add-discovery-address" {
		t.Fatalf("audit action %q", resp.Msg.GetAudit().GetAction())
	}

	d := storedDiscovery(t, k8s)
	if d == nil || len(d.Addresses) != 2 || d.Addresses[1] != addrB || len(d.CIDRs) != 1 {
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

	if d := storedDiscovery(t, k8s); d == nil || len(d.Addresses) != 1 {
		t.Fatalf("got %+v", d)
	}
}

func TestAlreadyConfiguredAddressesComeFromThatClusterEvents(t *testing.T) {
	c := discoveryCluster(nil)
	ev := func(name, cluster, reason, ep string) *corev1.Event {
		return &corev1.Event{
			Name: name, Namespace: testNS, Annotations: map[string]string{annotationAlreadyConfiguredEndpoint: ep},
			InvolvedObject: corev1.ObjectReference{Kind: "TalosCluster", Name: cluster, Namespace: testNS},
			Reason:         reason,
		}
	}
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c,
		ev("a", "c", reasonAlreadyConfigured, "10.0.0.9:50000"),
		ev("b", "c", reasonAlreadyConfigured, "10.0.0.9:50000"),
		ev("d", "c", "WeakIdentity", "10.0.0.8:50000"),
		ev("e", "other", reasonAlreadyConfigured, "10.0.0.7:50000"),
	).Build()

	got := (&Server{Client: k8s}).alreadyConfiguredAddresses(context.Background(), c)
	if len(got) != 1 || got[0] != "10.0.0.9:50000" {
		t.Fatalf("want only the deduplicated AlreadyConfigured endpoint of cluster c, got %v", got)
	}

	resp, err := (&Server{Client: k8s}).AddDiscoveryAddress(context.Background(), addAddrReq("10.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}

	if a := resp.Msg.GetCluster().GetAlreadyConfiguredAddresses(); len(a) != 1 {
		t.Fatalf("the add-by-IP response should carry them, got %v", a)
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
