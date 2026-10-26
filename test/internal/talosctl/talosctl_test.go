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

//nolint:goconst // table-driven argument lists
package talosctl

import "testing"

func TestCheckTargets(t *testing.T) {
	bad := [][]string{
		{"get", "disks"}, // no --nodes: would use a talosconfig default
		{"get", "disks", "--nodes", ""},
		{"get", "disks", "--nodes", "192.168.0.201"},
		{"get", "disks", "--nodes", "10.0.0.5", "--endpoints", "192.168.0.202"},
		{"health", "--control-plane-nodes", "10.0.0.5,192.168.0.203", "--nodes", "10.0.0.5"},
	}
	for _, a := range bad {
		if checkTargets(a) == nil {
			t.Errorf("%v: want refusal", a)
		}
	}
	if err := checkTargets([]string{"get", "disks", "--nodes", "10.244.1.7"}); err != nil {
		t.Errorf("explicit non-protected node refused: %v", err)
	}
}
