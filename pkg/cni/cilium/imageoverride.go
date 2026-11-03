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
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The image keys of a Cilium patch (ADR 0115, amendment 2026-11-03) and the chart values each one
// overrides. The chart's image helpers print an "override" reference as it is, digest included, in
// place of the repository, tag and digest it would otherwise compose.
const (
	ImageAgent       = "agent"
	ImageOperator    = "operator"
	ImageHubbleRelay = "hubble-relay"
)

// The values keys on the path to an image's override.
const (
	keyImage    = "image"
	keyOverride = "override"
)

// imageOverrideKeys is the values path of each image key.
var imageOverrideKeys = map[string][]string{
	ImageAgent:       {keyImage, keyOverride},
	ImageOperator:    {"operator", keyImage, keyOverride},
	ImageHubbleRelay: {"hubble", "relay", keyImage, keyOverride},
}

// ImageOverrideActive is the Cilium patch the release runs on the embedded chart, or nil when there is
// none. The override stops applying once the embedded chart reaches the patch: a newer Ichor embeds the
// patch's images in its chart, so the status is cleared by the controller and nothing reads it here.
func ImageOverrideActive(cluster *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCiliumImageOverride {
	if cluster.Status.CNI == nil || cluster.Status.CNI.ImageOverride == nil {
		return nil
	}

	o := cluster.Status.CNI.ImageOverride
	if semver.Compare(o.Version, "v"+ChartVersion) <= 0 {
		return nil
	}

	return o
}

// WithImageOverrides sets the image references of a patch in the chart values. Every image key must be
// known, and every reference must be pinned by a sha256 digest, so a tag alone never runs.
func WithImageOverrides(vals map[string]any, images map[string]string) error {
	for key, ref := range images {
		path, ok := imageOverrideKeys[key]
		if !ok {
			return fmt.Errorf("cilium: %q is not an image of a Cilium patch", key)
		}

		if !strings.Contains(ref, "@sha256:") {
			return fmt.Errorf("cilium: the %s image %q is not pinned by a sha256 digest", key, ref)
		}

		m := vals
		for _, p := range path[:len(path)-1] {
			next, ok := m[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[p] = next
			}

			m = next
		}

		m[path[len(path)-1]] = ref
	}

	return nil
}
