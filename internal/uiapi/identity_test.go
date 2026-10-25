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
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func TestStrongIdentity(t *testing.T) {
	cases := []struct {
		name     string
		identity ichorv1alpha1.TalosMachineIdentity
		want     bool
	}{
		{"complete tuple", ichorv1alpha1.TalosMachineIdentity{
			UUID: "u", Serial: "s", MACAddresses: []string{testMAC},
		}, true},
		{"missing UUID", ichorv1alpha1.TalosMachineIdentity{
			Serial: "s", MACAddresses: []string{testMAC},
		}, false},
		{"missing serial", ichorv1alpha1.TalosMachineIdentity{
			UUID: "u", MACAddresses: []string{testMAC},
		}, false},
		{"missing every MAC", ichorv1alpha1.TalosMachineIdentity{UUID: "u", Serial: "s"}, false},
		{"everything empty", ichorv1alpha1.TalosMachineIdentity{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := strongIdentity(tc.identity); got != tc.want {
				t.Fatalf("strongIdentity(%+v) = %v, want %v", tc.identity, got, tc.want)
			}
		})
	}
}
