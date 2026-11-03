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
	"github.com/maeshinshin/ichor/pkg/release"
)

// The Versions card reports what each component runs (ADR 0115). Talos, the contract and Kubernetes
// carry no state: they are not measured against the release set. Cilium is behind when the embedded
// chart is newer than the installed release.
func TestComponentVersionsReportTheRunningVersions(t *testing.T) {
	t.Parallel()

	c := &ichorv1alpha1.TalosCluster{
		Spec: ichorv1alpha1.TalosClusterSpec{
			TalosVersion: "v1.13.2", VersionContract: "v1.13", KubernetesVersion: "v1.36.2",
		},
		Status: ichorv1alpha1.TalosClusterStatus{CNI: &ichorv1alpha1.TalosClusterCNIStatus{
			Detected: "cilium", DetectedVersion: "v1.17.0", AvailableChartVersion: release.Pinned.Cilium,
		}},
	}

	got := map[string]*pbComponent{}
	for _, v := range componentVersionsToPB(c) {
		got[v.GetName()] = &pbComponent{current: v.GetCurrent(), target: v.GetTarget(), state: v.GetState()}
	}

	for name, want := range map[string]pbComponent{
		componentTalos:    {current: "v1.13.2", target: release.Pinned.Talos},
		componentContract: {current: "v1.13", target: release.Pinned.VersionContract},
		componentK8s:      {current: "v1.36.2", target: release.Pinned.Kubernetes},
		componentCilium:   {current: "1.17.0", target: release.Pinned.Cilium, state: string(release.Behind)},
	} {
		g := got[name]
		if g == nil || *g != want {
			t.Errorf("%s = %+v, want %+v", name, g, want)
		}
	}
}

// pbComponent is the part of a ComponentVersion the test compares.
type pbComponent struct{ current, target, state string }
