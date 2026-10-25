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
	"fmt"
	"strconv"
	"strings"
	"testing"
)

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
