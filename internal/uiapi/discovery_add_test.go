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
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestDiscoveryFindingsAreDerivedFromCurrentState(t *testing.T) {
	c := discoveryCluster(nil)
	now := metav1.Now()
	ev := func(name, reason, ep string, ago time.Duration) *corev1.Event {
		return &corev1.Event{
			Name: name, Namespace: testNS, Annotations: map[string]string{annotationAlreadyConfiguredEndpoint: ep},
			InvolvedObject: corev1.ObjectReference{Kind: "TalosCluster", Name: "c", Namespace: testNS},
			Reason:         reason, LastTimestamp: metav1.NewTime(now.Add(-ago)),
		}
	}
	machine := func(name, endpoint string, state string) *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{Name: name, Namespace: testNS, Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"}, Endpoint: endpoint, ApprovalState: state,
		}}
	}
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(c,
		// foreign device: stays AlreadyConfigured.
		ev("foreign", reasonAlreadyConfigured, "10.0.0.1:50000", time.Minute),
		// an orphan.
		ev("orphan", reasonOrphan, "10.0.0.2:50000", time.Minute),
		// AlreadyConfigured long ago, Orphan since: listed once, as an orphan.
		ev("old", reasonAlreadyConfigured, "10.0.0.3:50000", 3*time.Hour),
		ev("new", reasonOrphan, "10.0.0.3:50000", time.Minute),
		// stale: the machine became pending again at this address.
		ev("stale", reasonAlreadyConfigured, "10.0.0.4:50000", 3*time.Hour),
		machine("pending", "10.0.0.4:50000", ichorv1alpha1.MachineApprovalStatePending),
		// stale orphan: an approved machine has it now.
		ev("stale2", reasonOrphan, "10.0.0.5:50000", 3*time.Hour),
		machine("approved", "10.0.0.5:50000", ichorv1alpha1.MachineApprovalStateApproved),
		// a rejected machine does not hide the address.
		ev("rej", reasonAlreadyConfigured, "10.0.0.6:50000", time.Minute),
		machine("rejected", "10.0.0.6:50000", ichorv1alpha1.MachineApprovalStateRejected),
	).Build()

	configured, orphans := (&Server{Client: k8s}).discoveryFindings(context.Background(), c)
	if !slices.Equal(configured, []string{"10.0.0.1:50000", "10.0.0.6:50000"}) {
		t.Fatalf("already configured: %v", configured)
	}

	if !slices.Equal(orphans, []string{"10.0.0.2:50000", "10.0.0.3:50000"}) {
		t.Fatalf("orphans: %v", orphans)
	}
}
