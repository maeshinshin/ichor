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
	"slices"
	"testing"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	cidrA = "10.1.0.0/24"
	cidrB = "10.2.0.0/24"
	addrB = "10.0.0.56"
)

func storedDiscovery(t *testing.T, k8s client.Client) *ichorv1alpha1.TalosClusterDiscovery {
	t.Helper()

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	return got.Spec.Discovery
}

func TestAddDiscoveryCidrAppendsOnlyThatRange(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}, CIDRs: []string{cidrA}})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()

	resp, err := (&Server{Client: k8s}).AddDiscoveryCidr(context.Background(), connect.NewRequest(
		&pb.AddDiscoveryCidrRequest{Cluster: objectRef(testNS, "c"), Cidr: " 192.168.0.7/24 "}))
	if err != nil {
		t.Fatal(err)
	}

	if a := resp.Msg.GetAudit(); a.GetAction() != "add-discovery-cidr" || a.GetReason() != "" {
		t.Fatalf("audit %v", a)
	}

	d := storedDiscovery(t, k8s)
	if !slices.Equal(d.CIDRs, []string{cidrA, "192.168.0.0/24"}) || !slices.Equal(d.Addresses, []string{knownAddr}) {
		t.Fatalf("want the masked range appended and the addresses kept, got %+v", d)
	}

	if len(resp.Msg.GetCluster().GetDiscoveryCidrs()) != 2 {
		t.Fatalf("the response should carry the stored cluster, got %v", resp.Msg.GetCluster())
	}
}

func TestAddDiscoveryCidrRefusesBadInput(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{
		CIDRs: []string{cidrA, cidrB, "10.3.0.0/24"},
	})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}

	for _, bad := range []string{"", "10.0.0.5", "10.0.0.0/16", "10.0.0.0/23", "fe80::/64", "nope/24", "10.1.0.9/24"} {
		_, err := s.AddDiscoveryCidr(context.Background(), connect.NewRequest(
			&pb.AddDiscoveryCidrRequest{Cluster: objectRef(testNS, "c"), Cidr: bad}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("%q: want InvalidArgument, got %v", bad, err)
		}
	}

	if _, err := s.AddDiscoveryCidr(context.Background(), connect.NewRequest(
		&pb.AddDiscoveryCidrRequest{Cluster: objectRef(testNS, "c"), Cidr: "10.4.0.0/28"})); err != nil {
		t.Fatal(err)
	}

	_, err := s.AddDiscoveryCidr(context.Background(), connect.NewRequest(
		&pb.AddDiscoveryCidrRequest{Cluster: objectRef(testNS, "c"), Cidr: "10.5.0.0/24"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("the fifth range: want InvalidArgument, got %v", err)
	}

	if n := len(storedDiscovery(t, k8s).CIDRs); n != 4 {
		t.Fatalf("want 4 ranges stored, got %d", n)
	}
}

func TestRemoveDiscoveryAddressAndCidr(t *testing.T) {
	c := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{
		Addresses: []string{knownAddr, addrB}, CIDRs: []string{cidrA, cidrB},
	})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c).Build()
	s := &Server{Client: k8s}
	ref := objectRef(testNS, "c")

	ra, err := s.RemoveDiscoveryAddress(context.Background(), connect.NewRequest(
		&pb.RemoveDiscoveryAddressRequest{Cluster: ref, Address: knownAddr}))
	if err != nil || ra.Msg.GetAudit().GetAction() != "remove-discovery-address" {
		t.Fatalf("%v %v", ra, err)
	}

	rc, err := s.RemoveDiscoveryCidr(context.Background(), connect.NewRequest(
		&pb.RemoveDiscoveryCidrRequest{Cluster: ref, Cidr: cidrA}))
	if err != nil || rc.Msg.GetAudit().GetAction() != "remove-discovery-cidr" {
		t.Fatalf("%v %v", rc, err)
	}

	d := storedDiscovery(t, k8s)
	if !slices.Equal(d.Addresses, []string{addrB}) || !slices.Equal(d.CIDRs, []string{cidrB}) {
		t.Fatalf("got %+v", d)
	}

	_, err = s.RemoveDiscoveryAddress(context.Background(), connect.NewRequest(
		&pb.RemoveDiscoveryAddressRequest{Cluster: ref, Address: knownAddr}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("an address not listed: want InvalidArgument, got %v", err)
	}

	_, err = s.RemoveDiscoveryCidr(context.Background(), connect.NewRequest(
		&pb.RemoveDiscoveryCidrRequest{Cluster: ref, Cidr: "10.9.0.0/24"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("a range not listed: want InvalidArgument, got %v", err)
	}
}

func TestDiscoveryEditsAreWrittenAsTheCaller(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(discoveryCluster(
		&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{knownAddr}, CIDRs: []string{cidrA}})).Build()
	user := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Group: ichorv1alpha1.GroupVersion.Group, Resource: resourceTalosClusters}, obj.GetName(), nil)
		},
	})
	s := &Server{Client: base, Impersonate: func(Identity) (client.Client, error) { return user, nil }}
	ctx := asUser(context.Background())
	ref := objectRef(testNS, "c")

	_, e1 := s.AddDiscoveryCidr(ctx, connect.NewRequest(&pb.AddDiscoveryCidrRequest{Cluster: ref, Cidr: cidrB}))
	_, e2 := s.RemoveDiscoveryAddress(ctx, connect.NewRequest(&pb.RemoveDiscoveryAddressRequest{Cluster: ref, Address: knownAddr}))
	_, e3 := s.RemoveDiscoveryCidr(ctx, connect.NewRequest(&pb.RemoveDiscoveryCidrRequest{Cluster: ref, Cidr: cidrA}))

	for i, err := range []error{e1, e2, e3} {
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("call %d: want PermissionDenied, got %v", i, err)
		}
	}

	d := storedDiscovery(t, base)
	if len(d.Addresses) != 1 || len(d.CIDRs) != 1 {
		t.Fatalf("nothing may change, got %+v", d)
	}
}
