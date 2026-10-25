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

import "testing"

// TestIsStrongIdentity is the canonical test's identity-tuple
// rule, moved here from internal/uiapi when the rule itself
// moved here: internal/controller.strongIdentity and
// internal/uiapi.machine_service.go's own call both exercise this same
// method now, so this is the one place its truth table needs testing.
func TestIsStrongIdentity(t *testing.T) {
	const testMAC = "aa:bb:cc:dd:ee:ff"

	cases := []struct {
		name     string
		identity TalosMachineIdentity
		want     bool
	}{
		{"complete tuple", TalosMachineIdentity{
			UUID: "u", Serial: "s", MACAddresses: []string{testMAC},
		}, true},
		{"missing UUID", TalosMachineIdentity{
			Serial: "s", MACAddresses: []string{testMAC},
		}, false},
		{"missing serial", TalosMachineIdentity{
			UUID: "u", MACAddresses: []string{testMAC},
		}, false},
		{"missing every MAC", TalosMachineIdentity{UUID: "u", Serial: "s"}, false},
		{"everything empty", TalosMachineIdentity{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.identity.IsStrongIdentity(); got != tc.want {
				t.Fatalf("IsStrongIdentity(%+v) = %v, want %v", tc.identity, got, tc.want)
			}
		})
	}
}
