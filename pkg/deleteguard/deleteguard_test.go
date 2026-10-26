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

package deleteguard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func newCountTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("adding corev1 to scheme: %v", err)
	}
	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("adding ichorv1alpha1 to scheme: %v", err)
	}

	return s
}

func newCountTestMachine(namespace, name, clusterName, role, approvalState string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Namespace: namespace, Name: name,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "00000000-0000-0000-0000-00000000" + name},
			Role:          role,
			ApprovalState: approvalState,
		},
	}
}

func TestCheckDelete(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{name: "no annotations", annotations: nil, wantErr: true},
		{name: "annotation missing", annotations: map[string]string{"other": "x"}, wantErr: true},
		{name: "annotation false", annotations: map[string]string{AllowDeleteAnnotation: "false"}, wantErr: true},
		{name: "annotation true", annotations: map[string]string{AllowDeleteAnnotation: AllowDeleteValue}, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDelete(tc.annotations)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckDelete(%v) = nil, want error", tc.annotations)
				}
				if !strings.Contains(err.Error(), AllowDeleteAnnotation) {
					t.Fatalf("CheckDelete(%v) error %q does not name the annotation", tc.annotations, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckDelete(%v) = %v, want nil", tc.annotations, err)
			}
		})
	}
}

func TestCheckControlPlaneQuorum(t *testing.T) {
	cases := []struct {
		currentControlPlanes int
		wantErr              bool
	}{
		{currentControlPlanes: 1, wantErr: true},  // last one: 1 -> 0
		{currentControlPlanes: 2, wantErr: true},  // quorum of 2 is 2: 2 -> 1 breaks it
		{currentControlPlanes: 3, wantErr: false}, // quorum of 3 is 2: 3 -> 2 holds
		{currentControlPlanes: 4, wantErr: false}, // quorum of 4 is 3: 4 -> 3 holds
		{currentControlPlanes: 5, wantErr: false}, // quorum of 5 is 3: 5 -> 4 holds
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("n=%d", tc.currentControlPlanes), func(t *testing.T) {
			err := CheckControlPlaneQuorum(tc.currentControlPlanes)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckControlPlaneQuorum(%d) = nil, want error", tc.currentControlPlanes)
				}
				after := tc.currentControlPlanes - 1
				msg := err.Error()
				if !strings.Contains(msg, strconv.Itoa(tc.currentControlPlanes)) || !strings.Contains(msg, strconv.Itoa(after)) {
					t.Fatalf("CheckControlPlaneQuorum(%d) error %q does not say %d and %d",
						tc.currentControlPlanes, msg, tc.currentControlPlanes, after)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckControlPlaneQuorum(%d) = %v, want nil", tc.currentControlPlanes, err)
			}
		})
	}
}

func TestCheckControlPlaneCount(t *testing.T) {
	cases := []struct {
		current, desired int
		wantErr          bool
	}{
		{current: 3, desired: 3, wantErr: true},  // 3 -> 2, below the declared 3
		{current: 4, desired: 3, wantErr: false}, // 4 -> 3, meets the declared 3 (the add-then-remove case)
		{current: 1, desired: 1, wantErr: true},  // 1 -> 0, below the declared 1
		{current: 5, desired: 5, wantErr: true},  // 5 -> 4, below the declared 5
		{current: 6, desired: 5, wantErr: false}, // 6 -> 5, meets the declared 5
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("current=%d,desired=%d", tc.current, tc.desired), func(t *testing.T) {
			err := CheckControlPlaneCount(tc.current, tc.desired)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CheckControlPlaneCount(%d, %d) = nil, want error", tc.current, tc.desired)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckControlPlaneCount(%d, %d) = %v, want nil", tc.current, tc.desired, err)
			}
		})
	}
}

// TestAllowDeleteDoesNotOverrideQuorum is the roadmap's explicit requirement:
// the allow-delete annotation alone must not let the last control plane (or
// any quorum-breaking deletion) go. CheckDelete and CheckControlPlaneQuorum
// are separate checks the webhook runs both of; this documents that they
// really are independent, not that annotations feed into the quorum check.
func TestAllowDeleteDoesNotOverrideQuorum(t *testing.T) {
	annotations := map[string]string{AllowDeleteAnnotation: AllowDeleteValue}

	if err := CheckDelete(annotations); err != nil {
		t.Fatalf("CheckDelete with the annotation set = %v, want nil", err)
	}

	if err := CheckControlPlaneQuorum(1); err == nil {
		t.Fatal("CheckControlPlaneQuorum(1) = nil, want error for the last control plane despite the annotation")
	}

	if err := CheckControlPlaneQuorum(2); err == nil {
		t.Fatal("CheckControlPlaneQuorum(2) = nil, want error for a quorum-breaking delete despite the annotation")
	}
}

// TestCountApprovedControlPlanes checks every exclusion the quorum count
// depends on: role, approval state, cluster and namespace. Uses a fake
// client (uncached by construction, like the real callers' APIReader)
// rather than envtest, since this is pure List-and-filter logic with no
// webhook or controller behavior to exercise.
func TestCountApprovedControlPlanes(t *testing.T) {
	scheme := newCountTestScheme(t)

	const (
		cp       = "controlplane"
		worker   = "worker"
		approved = ichorv1alpha1.MachineApprovalStateApproved
		pending  = ichorv1alpha1.MachineApprovalStatePending
	)

	objs := []client.Object{
		newCountTestMachine("default", "cp1", "west", cp, approved),
		newCountTestMachine("default", "cp2", "west", cp, approved),
		newCountTestMachine("default", "cp3", "west", cp, pending),
		newCountTestMachine("default", "worker1", "west", worker, approved),
		newCountTestMachine("default", "cp-other-cluster", "east", cp, approved),
		newCountTestMachine("other-ns", "cp-other-ns", "west", cp, approved),
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	got, err := CountApprovedControlPlanes(context.Background(), fakeClient, "default", "west")
	if err != nil {
		t.Fatalf("CountApprovedControlPlanes: %v", err)
	}

	// Only cp1 and cp2 qualify: cp3 is pending, worker1 is not a control
	// plane, cp-other-cluster belongs to "east", and cp-other-ns is in a
	// different namespace.
	if got != 2 {
		t.Fatalf("CountApprovedControlPlanes = %d, want 2", got)
	}
}

func TestCheckControlPlaneApproval(t *testing.T) {
	for _, tc := range []struct {
		current, desired int
		wantErr          bool
	}{
		{0, 1, false}, {1, 1, false}, // first approval, then the replacement slot
		{2, 1, true},
		{2, 3, false}, {3, 3, false}, // scale-up and 3->4
		{4, 3, true},
		{5, 5, false}, {6, 5, true},
	} {
		if err := CheckControlPlaneApproval(tc.current, tc.desired); (err != nil) != tc.wantErr {
			t.Errorf("CheckControlPlaneApproval(%d, %d) = %v, wantErr %v", tc.current, tc.desired, err, tc.wantErr)
		}
	}
}

func TestCheckLastWorker(t *testing.T) {
	for _, tc := range []struct {
		unschedulable bool
		after         int
		wantErr       bool
	}{
		{true, 0, true}, {true, 1, false}, {false, 0, false},
	} {
		if err := CheckLastWorker(tc.unschedulable, tc.after); (err != nil) != tc.wantErr {
			t.Errorf("CheckLastWorker(%v, %d) = %v, wantErr %v", tc.unschedulable, tc.after, err, tc.wantErr)
		}
	}
}

func TestCountApprovedWorkers(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newCountTestScheme(t)).WithObjects(
		newCountTestMachine("default", "w1", "west", "worker", ichorv1alpha1.MachineApprovalStateApproved),
		newCountTestMachine("default", "w2", "west", "worker", ichorv1alpha1.MachineApprovalStatePending),
		newCountTestMachine("default", "cp1", "west", "controlplane", ichorv1alpha1.MachineApprovalStateApproved),
	).Build()

	got, err := CountApprovedWorkers(context.Background(), c, "default", "west")
	if err != nil || got != 1 {
		t.Fatalf("CountApprovedWorkers = %d, %v; want 1", got, err)
	}
}
