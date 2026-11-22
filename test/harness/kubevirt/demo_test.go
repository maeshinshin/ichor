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

package main

import (
	"slices"
	"strings"
	"testing"
)

// TestDownDemoPrefixIgnoresScale builds the objects of a 1x3 lab and a 3x2 lab and tears them down
// with no scale: all of them go, and a longer prefix, a prefix that nests and the base cache stay.
func TestDownDemoPrefixIgnoresScale(t *testing.T) {
	lab := func(p string, vms ...string) (out []string) {
		for _, v := range vms {
			out = append(out, p+"-"+v)
		}

		return out
	}
	vms := append(lab("lh", "cp", "w", "w2", "w3"), lab("big", "cp", "cp2", "cp3", "w", "w2")...)
	keep := []string{"lh2-cp", "lh-cp-x", "lhx-w", "xlh-cp", "ichor-e2e-talos-v1-9-abcd1234-base"}
	store := map[string][]string{"vm": append(slices.Clone(vms), keep...), "dv": {}}

	for _, v := range vms {
		store["dv"] = append(store["dv"], v+"-iso", v+"-system")
	}

	for _, k := range keep {
		store["dv"] = append(store["dv"], k+"-iso", k+"-system")
	}

	list := func(r string) ([]string, error) { return slices.Clone(store[r]), nil }
	del := func(r string, names []string) error {
		store[r] = slices.DeleteFunc(store[r], func(n string) bool { return slices.Contains(names, n) })

		return nil
	}

	for _, p := range []string{"lh", "big"} {
		if err := downDemoPrefix(p, list, del); err != nil {
			t.Fatal(err)
		}
	}

	for _, r := range []string{"vm", "dv"} {
		for _, n := range store[r] {
			if !slices.ContainsFunc(keep, func(k string) bool { return strings.HasPrefix(n, k) }) {
				t.Errorf("%s %s of a torn-down lab is left behind", r, n)
			}
		}
	}

	if len(store["vm"]) != len(keep) || len(store["dv"]) != 2*len(keep) {
		t.Errorf("another prefix's objects were touched: vm=%v dv=%v", store["vm"], store["dv"])
	}
}
