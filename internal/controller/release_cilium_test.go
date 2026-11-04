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

package controller

import (
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
)

// Cilium moves only with Ichor's embedded chart (ADR 0115, amendment 2026-11-04 (3)): the release check
// needs no Cilium registry, so the egress policy holds the chart, Talos and Kubernetes hosts only.
func TestReleaseCheckEgressHasNoCiliumRegistry(t *testing.T) {
	r := &ReleaseCheck{Repository: releasecheck.DefaultRepository}

	hosts, err := r.registryHosts()
	if err != nil {
		t.Fatal(err)
	}

	if slices.Contains(hosts, "quay.io") {
		t.Fatalf("the egress hosts %v include quay.io, but no Cilium image is listed or verified any more", hosts)
	}
}

// A status written by an earlier controller carries a Cilium patch list and its error. The next check
// drops them, so the UI never offers a patch that nothing would apply.
func TestUpgradeTargetsOfDropsTheCiliumListOfAnEarlierController(t *testing.T) {
	prev := &ichorv1alpha1.IchorUpgradeTargets{
		Cilium: []string{"v1.20.5"}, CiliumError: "quay unreachable",
	}

	got := upgradeTargetsOf(prev, metav1.Now(), listing{}, listing{})

	if len(got.Cilium) != 0 || got.CiliumError != "" {
		t.Fatalf("the Cilium list %v and error %q were kept", got.Cilium, got.CiliumError)
	}
}
