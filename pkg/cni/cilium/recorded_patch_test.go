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

package cilium

import (
	"reflect"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/values"
)

// recordedPatchCluster is a cluster whose status records a Cilium patch an earlier Ichor ran (revised): nothing sets
// a new one any more.
func recordedPatchCluster(version string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{}
	c.Name, c.Namespace = "c", "ns"
	c.Spec.ControlPlane.Count = 1
	c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{
		ImageOverride: &ichorv1alpha1.TalosCiliumImageOverride{
			Version: version,
			Images:  map[string]string{ImageAgent: "quay.io/cilium/cilium:" + version + "@sha256:" + strings.Repeat("1", 64)},
		},
	}

	return c
}

// A recorded patch newer than the embedded chart keeps being rendered, so an Ichor upgrade never steps Cilium back.
func TestEffectiveValuesRenderARecordedPatchNewerThanTheChart(t *testing.T) {
	c := recordedPatchCluster("v" + ChartVersion[:len(ChartVersion)-1] + "9" + "9")

	vals, err := EffectiveValuesForMode(c, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	got, _ := values.Get(vals, "image.override")
	if got != c.Status.CNI.ImageOverride.Images[ImageAgent] {
		t.Fatalf("image.override = %v, want the recorded agent image", got)
	}
}

// A recorded patch the embedded chart has reached (or passed) is not rendered: the chart's own images run.
func TestEffectiveValuesDropARecordedPatchTheChartReached(t *testing.T) {
	plain := recordedPatchCluster("v" + ChartVersion)
	plain.Status.CNI = nil

	want, err := EffectiveValuesForMode(plain, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"v" + ChartVersion, "v0.0.1"} {
		got, err := EffectiveValuesForMode(recordedPatchCluster(version), cni.KubeProxyEnabled)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("a recorded patch %s changed the values", version)
		}
	}
}
