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

package cli

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// cniKubeProxy is the only kubeProxy mode `ichor init` offers today:
// Talos's own default. Replacing it is a separate, later decision (a
// --kube-proxy flag the design's example invocation does not show), not
// something this command guesses at.
const cniKubeProxy = cni.KubeProxyEnabled

// cniCilium is the cilium --cni preset name (goconst: it is compared in
// several places).
const cniCilium = "cilium"

// cniBootstrap is everything step 3 ("cluster.inlineManifests carry...
// the CNI (unless flannel)") and step 5's CRs need from the CNI preset,
// computed once so both steps use the same values.
type cniBootstrap struct {
	// InlineManifest is appended to the rendered config's
	// cluster.inlineManifests.
	InlineManifest *typedconfig.ClusterInlineManifest

	// Patch is a cluster-layer TalosConfigPatch carrying the CNI's
	// TalosPatches documents; nil if the preset needs none.
	Patch *ichorv1alpha1.TalosConfigPatch
}

// buildCNIBootstrap calls preset's three bootstrap-relevant methods and
// assembles their output into the shapes init's rendering and CR-creation
// steps need. It never duplicates what pkg/cni already knows (ADR 0014's
// ponytail note on this unit: "CNI knowledge does not get rewritten in
// internal/cli") -- every byte here is exactly what the preset returned.
func buildCNIBootstrap(preset cilium.Preset, clusterName, namespace string) (*cniBootstrap, error) {
	out := &cniBootstrap{}

	// The manifest goes into the first control plane's config: when it applies, that node is the only
	// Node, whatever control-plane count the cluster is meant to reach (ADR 0113 amendment 2026-11-04).
	manifest, err := preset.WithNodes(1).BootstrapManifest(cniKubeProxy)
	if err != nil {
		return nil, fmt.Errorf("init: rendering cni bootstrap manifest: %w", err)
	}

	if len(manifest) > 0 {
		out.InlineManifest = &typedconfig.ClusterInlineManifest{
			InlineManifestName:     cniKey,
			InlineManifestContents: string(manifest),
		}
	}

	docs, err := preset.TalosPatches(cniKubeProxy)
	if err != nil {
		return nil, fmt.Errorf("init: computing cni Talos patches: %w", err)
	}

	if len(docs) > 0 {
		out.Patch = &ichorv1alpha1.TalosConfigPatch{
			Name: clusterName + "-cni", Namespace: namespace,
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: clusterName},
				Layer:      cniPatchLayer,
				Documents:  docs,
			},
		}
	}

	return out, nil
}
