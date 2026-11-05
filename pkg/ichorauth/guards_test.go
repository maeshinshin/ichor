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

package ichorauth

import "testing"

func TestUngrantedRole(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		groups, current, want []string
		role                  string
	}{
		{name: "admin may grant any role", groups: []string{"ichor:admin"}, want: []string{"admin", "custom"}},
		{name: "a held role", groups: []string{"ichor:operator"}, want: []string{"operator"}},
		{name: "a role not held", groups: []string{"ichor:operator"}, want: []string{"operator", "admin"}, role: "admin"},
		{name: "a role the target already has is no grant", groups: []string{"ichor:viewer"}, current: []string{"admin"}, want: []string{"admin"}},
		{name: "a group of another prefix holds nothing", groups: []string{"oidc:admin"}, want: []string{"admin"}, role: "admin"},
		{name: "no roles wanted", groups: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := UngrantedRole(tc.groups, tc.current, tc.want); got != tc.role {
				t.Fatalf("UngrantedRole() = %q, want %q", got, tc.role)
			}
		})
	}
}

func TestIsEnabledAdmin(t *testing.T) {
	for _, tc := range []struct {
		roles    []string
		disabled bool
		want     bool
	}{
		{roles: []string{"admin"}, want: true},
		{roles: []string{"viewer", "admin"}, want: true},
		{roles: []string{"admin"}, disabled: true},
		{roles: []string{"operator"}},
		{},
	} {
		if got := IsEnabledAdmin(tc.roles, tc.disabled); got != tc.want {
			t.Errorf("IsEnabledAdmin(%v, %v) = %v, want %v", tc.roles, tc.disabled, got, tc.want)
		}
	}
}
