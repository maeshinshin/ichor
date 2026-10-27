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
	"regexp"
	"strings"
	"testing"
)

func TestPRBranchNameCarriesTheNamespace(t *testing.T) {
	if got := prBranchName("approve", "prod", "m1"); got != "ichor/approve-prod-m1" {
		t.Fatalf("got %q", got)
	}

	if prBranchName("approve", "a", "m1") == prBranchName("approve", "b", "m1") {
		t.Fatal("same name in two namespaces must not share a branch")
	}
}

func TestPRBranchNameIsAValidBoundedRef(t *testing.T) {
	valid := regexp.MustCompile(`^ichor/[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

	cases := [][3]string{
		{"update", "ns", "x.lock"},
		{"update", "ns", "a..b"},
		{"update", "ns", "trailing."},
		{"update", "ns", strings.Repeat("a", 253)},
		{"update", strings.Repeat("n", 63), strings.Repeat("a", 253)},
	}

	seen := map[string]bool{}

	for _, c := range cases {
		got := prBranchName(c[0], c[1], c[2])
		if !valid.MatchString(got) || len(got) > len("ichor/")+maxBranchName {
			t.Errorf("prBranchName(%v) = %q: invalid or too long", c, got)
		}

		if seen[got] {
			t.Errorf("prBranchName(%v) = %q collides", c, got)
		}

		seen[got] = true
	}

	// Two long names that agree up to the cut still differ.
	long := strings.Repeat("a", 200)
	if prBranchName("update", "ns", long+"1") == prBranchName("update", "ns", long+"2") {
		t.Fatal("long names that differ only past the cut must not collide")
	}

	// Deterministic.
	if prBranchName("update", "ns", long) != prBranchName("update", "ns", long) {
		t.Fatal("not deterministic")
	}
}
