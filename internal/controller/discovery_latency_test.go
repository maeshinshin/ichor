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

package controller

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestFreshAddressWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	r := &TalosClusterReconciler{now: func() time.Time { return now }}

	cl := &ichorv1alpha1.TalosCluster{}
	cl.Namespace, cl.Name = "ns", "c"
	cl.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: []string{"192.0.2.1"}}

	if !r.freshAddress(cl) {
		t.Fatal("a just-added address must be fresh")
	}

	now = now.Add(discoveryFreshWindow - time.Second)

	if !r.freshAddress(cl) {
		t.Fatal("still inside the window")
	}

	now = now.Add(2 * time.Second)

	if r.freshAddress(cl) {
		t.Fatal("past the window the normal interval applies")
	}

	cl.Spec.Discovery.CIDRs = []string{"192.0.2.0/30"}

	if !r.freshAddress(cl) {
		t.Fatal("a newly added CIDR is fresh again")
	}

	cl.Spec.Discovery.CIDRs = nil
	r.freshAddress(cl)

	if len(r.firstSeen) != 1 {
		t.Fatalf("a removed entry must be forgotten, have %v", r.firstSeen)
	}
}

// conflictOnce fails the first Update with a Conflict, as the other reconciler's write would.
type conflictOnce struct {
	client.Client
	n int
}

func (c *conflictOnce) Update(ctx context.Context, o client.Object, opts ...client.UpdateOption) error {
	if c.n++; c.n == 1 {
		return apierrors.NewConflict(schema.GroupResource{Group: "cilium.io", Resource: "ciliumnetworkpolicies"}, o.GetName(), nil)
	}

	return c.Client.Update(ctx, o, opts...)
}

func TestSettleAfterCountsAnotherWritersWrite(t *testing.T) {
	e := &TalosEgress{Settle: 200 * time.Millisecond}
	e.lastWrite = time.Now()

	start := time.Now()
	e.SettleAfter(context.Background())

	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("returned after %v, want it to wait out the settle time", d)
	}

	start = time.Now()
	e.SettleAfter(context.Background())

	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("waited %v after the settle time had passed", d)
	}
}
