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
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The UI API added an address to the cluster's discovery list for each request. A request that did not
// end Done (failed, expired, deleted) must not leave it there; an address the user added themselves,
// one a TalosMachine came from and one of a request that is Done or still running stay.
func TestReclaimAddressesOfFailedRequestsAreTakenBackOut(t *testing.T) {
	const mine = "10.0.0.9"

	cluster := discoveryCluster(&ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{mine}})
	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(cluster).
		WithStatusSubresource(&ichorv1alpha1.MachineReclaim{}).Build()
	s := &Server{Client: k8s, ControllerNamespace: testNS,
		Impersonate: func(Identity) (client.Client, error) {
			return reviewing(k8s, true, &[]authv1.ResourceAttributes{}), nil
		}}
	ctx := ContextWithIdentity(context.Background(), Identity{User: bgUser})

	create := func(addr string) string {
		t.Helper()

		res, err := s.CreateReclaim(ctx, connect.NewRequest(&pb.CreateReclaimRequest{
			Address: addr, Talosconfig: []byte(reclaimMarker), Cluster: objectRef(testNS, "c"),
		}))
		if err != nil {
			t.Fatal(err)
		}

		return res.Msg.GetReclaim().GetRef().GetName()
	}

	phase := func(name string, p ichorv1alpha1.MachineReclaimPhase) {
		t.Helper()

		var mr ichorv1alpha1.MachineReclaim
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: name}, &mr); err != nil {
			t.Fatal(err)
		}

		mr.Status.Phase = p
		if err := k8s.Status().Update(ctx, &mr); err != nil {
			t.Fatal(err)
		}
	}

	failedOwn := create(mine) // already listed by the user: never recorded, never removed
	failed := create("10.0.0.5")
	expired := create("10.0.0.4")
	withMachine := create("10.0.0.6")
	deleted := create("10.0.0.7")
	done := create("10.0.0.8")
	_ = create("10.0.0.3") // still running

	phase(failedOwn, ichorv1alpha1.MachineReclaimFailed)
	phase(failed, ichorv1alpha1.MachineReclaimFailed)
	phase(expired, ichorv1alpha1.MachineReclaimExpired)
	phase(withMachine, ichorv1alpha1.MachineReclaimFailed)
	phase(done, ichorv1alpha1.MachineReclaimDone)

	if err := k8s.Delete(ctx, &ichorv1alpha1.MachineReclaim{Name: deleted, Namespace: testNS}); err != nil {
		t.Fatal(err)
	}

	if err := k8s.Create(ctx, &ichorv1alpha1.TalosMachine{
		Name: "m", Namespace: testNS,
		Spec: ichorv1alpha1.TalosMachineSpec{ClusterRef: corev1.LocalObjectReference{Name: "c"}, Endpoint: "10.0.0.6:50000"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetReclaim(ctx, connect.NewRequest(&pb.GetReclaimRequest{Reclaim: objectRef(testNS, failed)})); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "c"}, &got); err != nil {
		t.Fatal(err)
	}

	want := []string{mine, "10.0.0.6", "10.0.0.8", "10.0.0.3"}
	slices.Sort(want)

	have := slices.Clone(got.Spec.Discovery.Addresses)
	slices.Sort(have)

	if !slices.Equal(have, want) {
		t.Fatalf("discovery addresses %v, want %v", have, want)
	}
}
