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

package reclaim

import (
	"context"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A pending machine is only ever made from a node seen in maintenance mode, so for a node that is
// configured now it is stale and must not stop a reclaim. An approved one still does.
func TestLoadKnownIgnoresStalePendingMachines(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	mk := func(name, state string) *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{
			Name: name, Namespace: "default",
			Spec: ichorv1alpha1.TalosMachineSpec{
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + name},
				ApprovalState: state,
			},
		}
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		mk("approved", ichorv1alpha1.MachineApprovalStateApproved),
		mk("pending", ichorv1alpha1.MachineApprovalStatePending),
		mk("unset", ""),
		mk("rejected", ichorv1alpha1.MachineApprovalStateRejected),
	).Build()

	k, err := LoadKnown(context.Background(), c, "ichor-system")
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"uuid-approved"}; !slices.Equal(k.MachineUUIDs, want) {
		t.Fatalf("MachineUUIDs = %v, want %v", k.MachineUUIDs, want)
	}
}
