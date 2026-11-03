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

package release

import (
	"slices"
	"testing"
)

// The Cilium patches the list offers: newer than the running release,
// newer than the embedded chart, and compatible with the cluster's Kubernetes in the Cilium minor's range.
func TestCiliumChoicesOffersTheNewerPatchesThatTheChartDoesNotReach(t *testing.T) {
	tags := []string{"v1.20.5", "v1.20.6", "v1.20.4", "v1.21.0", "v1.20.7-rc.1"}

	got := CiliumChoices("v1.20.4", "1.20.4", "v1.36.6", tags)
	if want := []string{"v1.20.6", "v1.20.5"}; !slices.Equal(got.Patch, want) {
		t.Fatalf("patches = %v, want %v", got.Patch, want)
	}

	if len(got.Recommended) != 0 || len(got.NextMinor) != 0 {
		t.Errorf("recommended = %v, next minor = %v; a Cilium patch has neither", got.Recommended, got.NextMinor)
	}

	if got.Default() != "v1.20.6" {
		t.Errorf("default = %q, want the newest patch", got.Default())
	}
}

func TestCiliumChoicesLeavesOutThePatchTheRunningReleaseHas(t *testing.T) {
	got := CiliumChoices("v1.20.5", "1.20.4", "v1.36.6", []string{"v1.20.5", "v1.20.6"})
	if want := []string{"v1.20.6"}; !slices.Equal(got.Patch, want) {
		t.Fatalf("patches = %v, want %v", got.Patch, want)
	}
}

func TestCiliumChoicesStopsOnceTheChartReachesThePatch(t *testing.T) {
	got := CiliumChoices("v1.20.4", "1.20.6", "v1.36.6", []string{"v1.20.5", "v1.20.6"})
	if len(got.Patch) != 0 {
		t.Fatalf("patches = %v, want none: the embedded chart is at or past them", got.Patch)
	}
}

func TestCiliumChoicesRefusesAKubernetesTheCiliumDoesNotRun(t *testing.T) {
	got := CiliumChoices("v1.20.4", "1.20.4", "v1.31.9", []string{"v1.20.5"})
	if len(got.Patch) != 0 {
		t.Fatalf("patches = %v, want none: Cilium 1.20 is tested with Kubernetes 1.33 to 1.36", got.Patch)
	}
}
