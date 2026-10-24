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

// Package render turns a secrets bundle plus a cluster's config patches into
// a complete Talos machine configuration for one node, and hashes it. It
// makes no Talos API calls, touches no cluster, and never stores the
// rendered configuration anywhere (docs/design/architecture.md, "Machine
// config is never stored in a CR", principle 1 and 3): both the config and
// the hash are returned to the caller, which decides what to do with them.
package render

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// layerOrder maps a TalosConfigPatch's layer to its position in the
// bottom-up patch stack ("Patch layers" in docs/design/architecture.md).
// The system layer Ichor generates itself -- today, only the control-plane
// taint, folded into the base config below -- is never a TalosConfigPatch
// value, since the CRD's layer enum excludes it.
var layerOrder = map[string]int{
	"cluster":  1,
	"role":     2,
	"selector": 3,
	"machine":  4,
}

// Render builds the complete Talos machine configuration for machine m in
// cluster, using bundle for its secrets and whichever of candidates apply to
// m, then validates and hashes the result.
//
// It returns the rendered configuration as canonical YAML bytes and its
// hash as "sha256:<hex>". The hash is computed over those same bytes:
// machinery's own YAML encoder sorts map keys on every encode (so Go's
// randomized map iteration order never leaks into the output), document
// order is whatever this function's own deterministic merge sequence
// produced, and struct field order is fixed by the Go type -- so identical
// inputs always encode to identical bytes, in this process or any other.
func Render(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	candidates []ichorv1alpha1.TalosConfigPatch,
) ([]byte, string, error) {
	if bundle == nil {
		return nil, "", fmt.Errorf("render: secrets bundle is required")
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return nil, "", fmt.Errorf("render: parsing versionContract: %w", err)
	}

	machineType, err := machine.ParseType(m.Spec.Role)
	if err != nil {
		return nil, "", fmt.Errorf("render: parsing machine role: %w", err)
	}

	cfg, err := baseConfig(cluster, bundle, contract, machineType)
	if err != nil {
		return nil, "", err
	}

	ordered, err := selectPatches(cluster, m, candidates)
	if err != nil {
		return nil, "", err
	}

	cfg, err = applyPatches(cfg, ordered)
	if err != nil {
		return nil, "", fmt.Errorf("render: applying patches: %w", err)
	}

	if _, err := cfg.ValidateAsClient(clientRuntimeMode{}); err != nil {
		return nil, "", fmt.Errorf("render: validating rendered config: %w", err)
	}

	out, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		return nil, "", fmt.Errorf("render: encoding rendered config: %w", err)
	}

	sum := sha256.Sum256(out)

	return out, fmt.Sprintf("sha256:%x", sum), nil
}

// baseConfig generates the role-specific base config from the bundle
// (Configuration reconcile, step 1), with the version contract pinned from
// TalosCluster.spec.versionContract (principle 4) and the control-plane
// taint -- the only system-layer value these inputs support today -- folded
// in through machinery's own AllowSchedulingOnControlPlanes option, so that
// its representation (legacy field vs. KubeNodeConfig document) is gated by
// the same version contract machinery already understands, rather than
// Ichor guessing it a second time.
func baseConfig(
	cluster *ichorv1alpha1.TalosCluster,
	bundle *secrets.Bundle,
	contract *coreconfig.VersionContract,
	machineType machine.Type,
) (coreconfig.Provider, error) {
	endpoint := fmt.Sprintf("https://%s:%d", cluster.Spec.Endpoint.Address, constants.DefaultControlPlanePort)

	input, err := generate.NewInput(cluster.Name, endpoint, cluster.Spec.KubernetesVersion,
		generate.WithVersionContract(contract),
		generate.WithSecretsBundle(bundle),
		generate.WithAllowSchedulingOnControlPlanes(cluster.Spec.ControlPlane.Schedulable == "true"),
	)
	if err != nil {
		return nil, fmt.Errorf("render: preparing generator input: %w", err)
	}

	cfg, err := input.Config(machineType)
	if err != nil {
		return nil, fmt.Errorf("render: generating base config: %w", err)
	}

	return cfg, nil
}

// orderedPatch is a TalosConfigPatch that applies to the machine being
// rendered, carrying the sort key selectPatches computes for it.
type orderedPatch struct {
	layer    int
	priority int32
	name     string
	patch    *ichorv1alpha1.TalosConfigPatch
}

// selectPatches implements Configuration reconcile step 3 (which patches
// apply to this machine) and the layer/priority half of step 2: it filters
// candidates down to the ones that apply to m, then returns them in the
// order they must be applied -- bottom-up by layer, then by priority within
// a layer, then by name as a stable tie-break so that the result does not
// depend on the order candidates arrived in.
func selectPatches(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	candidates []ichorv1alpha1.TalosConfigPatch,
) ([]*ichorv1alpha1.TalosConfigPatch, error) {
	selected := make([]orderedPatch, 0, len(candidates))

	for i := range candidates {
		p := &candidates[i]

		ok, err := patchApplies(cluster, m, p)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		selected = append(selected, orderedPatch{
			layer:    layerOrder[p.Spec.Layer],
			priority: p.Spec.Priority,
			name:     p.Name,
			patch:    p,
		})
	}

	slices.SortFunc(selected, func(a, b orderedPatch) int {
		if a.layer != b.layer {
			return a.layer - b.layer
		}

		if a.priority != b.priority {
			return int(a.priority) - int(b.priority)
		}

		return strings.Compare(a.name, b.name)
	})

	ordered := make([]*ichorv1alpha1.TalosConfigPatch, len(selected))
	for i, sp := range selected {
		ordered[i] = sp.patch
	}

	return ordered, nil
}

// patchApplies reports whether p targets m: a cluster-layer patch applies to
// every machine in the cluster; role, selector and machine layers narrow by
// target. A patch from another cluster never applies.
func patchApplies(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	p *ichorv1alpha1.TalosConfigPatch,
) (bool, error) {
	if p.Namespace != cluster.Namespace || p.Spec.ClusterRef.Name != cluster.Name {
		return false, nil
	}

	switch p.Spec.Layer {
	case "cluster":
		return true, nil
	case "role":
		return p.Spec.Target != nil && p.Spec.Target.Role == m.Spec.Role, nil
	case "selector":
		return selectorMatches(p, m)
	case "machine":
		return p.Spec.Target != nil && p.Spec.Target.MachineRef != nil && p.Spec.Target.MachineRef.Name == m.Name, nil
	default:
		return false, nil
	}
}

func selectorMatches(p *ichorv1alpha1.TalosConfigPatch, m *ichorv1alpha1.TalosMachine) (bool, error) {
	if p.Spec.Target == nil || p.Spec.Target.LabelSelector == nil {
		return false, nil
	}

	sel, err := metav1.LabelSelectorAsSelector(p.Spec.Target.LabelSelector)
	if err != nil {
		return false, fmt.Errorf("render: patch %q: invalid labelSelector: %w", p.Name, err)
	}

	return sel.Matches(labels.Set(m.Spec.NodeLabels)), nil
}

// applyPatches merges each patch's documents onto cfg in the given order,
// one patch at a time, so that a later patch's non-zero fields win over an
// earlier one's (Configuration reconcile step 2).
func applyPatches(cfg coreconfig.Provider, ordered []*ichorv1alpha1.TalosConfigPatch) (coreconfig.Provider, error) {
	patches := make([]configpatcher.Patch, 0, len(ordered))

	for _, p := range ordered {
		docs := make([]config.Document, 0, len(p.Spec.Documents))

		for _, d := range p.Spec.Documents {
			doc, err := documentFromPatch(d)
			if err != nil {
				return nil, fmt.Errorf("render: patch %q: %w", p.Name, err)
			}

			docs = append(docs, doc)
		}

		if len(docs) == 0 {
			continue
		}

		c, err := container.New(docs...)
		if err != nil {
			return nil, fmt.Errorf("render: patch %q: %w", p.Name, err)
		}

		patches = append(patches, configpatcher.NewStrategicMergePatch(c))
	}

	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg), patches)
	if err != nil {
		return nil, err
	}

	return out.Config()
}

// documentFromPatch converts one TalosConfigPatchDocument to the machinery
// document its kind selects, through the typedconfig mirror type's own
// ToMachinery conversion (ADR 0003). The CRD's discriminator validation
// guarantees exactly one of the fields below is populated and that it
// matches Kind; the default case only guards against a value that bypassed
// that validation (e.g. in a unit test).
func documentFromPatch(d ichorv1alpha1.TalosConfigPatchDocument) (config.Document, error) {
	switch d.Kind {
	case "SysctlConfig":
		doc, err := d.SysctlConfig.ToMachinery()
		if err != nil {
			return nil, fmt.Errorf("sysctlConfig: %w", err)
		}

		return doc, nil
	case "KernelModuleConfig":
		doc, err := d.KernelModuleConfig.ToMachinery()
		if err != nil {
			return nil, fmt.Errorf("kernelModuleConfig: %w", err)
		}

		return doc, nil
	case "Config":
		doc, err := d.Config.ToMachinery()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}

		return doc, nil
	default:
		return nil, fmt.Errorf("unknown document kind %q", d.Kind)
	}
}

// clientRuntimeMode validates the rendered config the way a client outside
// Talos validates it (Container.ValidateAsClient's intended caller): never
// in a Talos container, and without requiring the deprecated .machine.install
// block, since Ichor does not render disk/install configuration yet (disk
// roles are a later phase, per the unit's scope).
type clientRuntimeMode struct{}

func (clientRuntimeMode) String() string        { return "ichor-render" }
func (clientRuntimeMode) RequiresInstall() bool { return false }
func (clientRuntimeMode) InContainer() bool     { return false }
