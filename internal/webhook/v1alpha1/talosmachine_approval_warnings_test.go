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
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestApprovalWarnings pins when an admission warning is raised (ADR 0111): on an approval that writes
// its picks, never on a change that leaves them alone, and never as a refusal.
func TestApprovalWarnings(t *testing.T) {
	const notDiscovered = "/dev/vdz"

	approved := func() *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{
			Spec: ichorv1alpha1.TalosMachineSpec{
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
				Install:       &ichorv1alpha1.TalosMachineInstall{Disk: ichorv1alpha1.TalosMachineDisk{DevPath: notDiscovered}},
			},
			Status: ichorv1alpha1.TalosMachineStatus{
				DiscoveredDisks: []ichorv1alpha1.TalosMachineDiscoveredDisk{{DevPath: "/dev/vda"}},
			},
		}
	}

	pending := approved()
	pending.Spec.ApprovalState = ""

	cases := []struct {
		name     string
		oldObj   *ichorv1alpha1.TalosMachine
		newObj   *ichorv1alpha1.TalosMachine
		wantWarn bool
	}{
		{"created approved with an undiscovered disk", nil, approved(), true},
		{"created pending is not an approval", nil, pending, false},
		{"approving with an undiscovered disk", pending, approved(), true},
		{"an unrelated edit to an approved machine is not judged again", approved(), approved(), false},
		{"moving the install disk to an undiscovered one", approved(), func() *ichorv1alpha1.TalosMachine {
			m := approved()
			m.Spec.Install.Disk = ichorv1alpha1.TalosMachineDisk{DevPath: "/dev/vdy"}

			return m
		}(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := approvalWarnings(tc.oldObj, tc.newObj)
			if (len(got) > 0) != tc.wantWarn {
				t.Fatalf("approvalWarnings() = %q, want a warning: %v", got, tc.wantWarn)
			}
		})
	}
}
