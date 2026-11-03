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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

const patchDigest = "sha256:" + "0000000000000000000000000000000000000000000000000000000000000001"

// patchImages are the references of the Cilium patch v1.20.5 for the three images the override covers.
func patchImages() map[string]string {
	return map[string]string{
		ImageAgent:       "quay.io/cilium/cilium:v1.20.5@" + patchDigest,
		ImageOperator:    "quay.io/cilium/operator-generic:v1.20.5@" + patchDigest,
		ImageHubbleRelay: "quay.io/cilium/hubble-relay:v1.20.5@" + patchDigest,
	}
}

func TestWithImageOverridesSetsTheChartPathsAndRefusesATagWithoutADigest(t *testing.T) {
	vals := map[string]any{}
	if err := WithImageOverrides(vals, patchImages()); err != nil {
		t.Fatalf("WithImageOverrides: %v", err)
	}

	if got := overrideAt(vals, "image", "override"); got != patchImages()[ImageAgent] {
		t.Errorf("image.override = %v", got)
	}

	if got := overrideAt(vals, "operator", "image", "override"); got != patchImages()[ImageOperator] {
		t.Errorf("operator.image.override = %v", got)
	}

	if got := overrideAt(vals, "hubble", "relay", "image", "override"); got != patchImages()[ImageHubbleRelay] {
		t.Errorf("hubble.relay.image.override = %v", got)
	}

	tagOnly := map[string]string{ImageAgent: "quay.io/cilium/cilium:v1.20.5"}
	if err := WithImageOverrides(map[string]any{}, tagOnly); err == nil {
		t.Error("an image given by tag alone was accepted")
	}

	other := map[string]string{"envoy": "quay.io/cilium/cilium-envoy:v1@" + patchDigest}
	if err := WithImageOverrides(map[string]any{}, other); err == nil {
		t.Error("an image key that is not a Cilium patch image was accepted")
	}
}

// overrideAt reads the values at path, nil when a key on the way is missing.
func overrideAt(vals map[string]any, path ...string) any {
	var cur any = vals

	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}

		cur = m[p]
	}

	return cur
}

// patchCluster is a cluster on the embedded chart whose status records the override.
func patchCluster(version string) *ichorv1alpha1.TalosCluster {
	c := &ichorv1alpha1.TalosCluster{}
	c.Name, c.Namespace = "c", "ns"
	c.Spec.ControlPlane.Count = 1
	c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{
		ImageOverride: &ichorv1alpha1.TalosCiliumImageOverride{Version: version, Images: patchImages()},
	}

	return c
}

func TestImageOverrideStopsOnceTheChartReachesIt(t *testing.T) {
	if ImageOverrideActive(patchCluster("v"+ChartVersion)) != nil {
		t.Error("an override for the embedded chart's own version is still active")
	}

	if ImageOverrideActive(patchCluster("v0.0.1")) != nil {
		t.Error("an override older than the embedded chart is still active")
	}

	if ImageOverrideActive(patchCluster("v"+ChartVersion[:len(ChartVersion)-1]+"9")) == nil {
		t.Error("a newer patch of the embedded chart's minor is not active")
	}
}

func TestEffectiveValuesCarryThePatchAndRenderItsDigests(t *testing.T) {
	c := patchCluster("v" + ChartVersion[:len(ChartVersion)-1] + "9")

	vals, err := EffectiveValuesForMode(c, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("EffectiveValuesForMode: %v", err)
	}

	out, err := renderChart(vals)
	if err != nil {
		t.Fatalf("renderChart: %v", err)
	}

	// hubble-relay renders only when Hubble is enabled, which the default preset does not do; its
	// path is covered by TestWithImageOverridesSetsTheChartPathsAndRefusesATagWithoutADigest.
	for _, key := range []string{ImageAgent, ImageOperator} {
		if !strings.Contains(string(out), patchImages()[key]) {
			t.Errorf("the rendered Cilium does not run %s", patchImages()[key])
		}
	}

	// Once the chart reaches the patch, the same cluster renders the chart's own images.
	c.Status.CNI.ImageOverride.Version = "v" + ChartVersion

	vals, err = EffectiveValuesForMode(c, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("EffectiveValuesForMode after the chart reached the patch: %v", err)
	}

	if img, ok := vals["image"].(map[string]any); ok && img["override"] != nil {
		t.Error("the agent image still carries the override after the chart reached the patch")
	}
}
