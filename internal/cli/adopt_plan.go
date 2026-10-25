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
	"bytes"
	"fmt"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	kindSysctlConfig       = "SysctlConfig"
	kindKernelModuleConfig = "KernelModuleConfig"
)

// adoptPlan is what buildAdoptPlan found for one machine: the
// TalosConfigPatch that closes the gap between its live configuration and
// a fresh render (nil if there was no gap, or if the gap could not be
// closed safely), plus the field paths -- names only, never values -- that
// this command left untouched either way.
type adoptPlan struct {
	Machine              string
	Patch                *ichorv1alpha1.TalosConfigPatch
	UnrepresentablePaths []string
}

// buildAdoptPlan diffs m's live configuration (currentCfg, as read from the
// Talos API) against a fresh render of cluster/m/bundle layered on top of
// whatever TalosConfigPatches already exist, and turns the difference into
// a single machine-layer TalosConfigPatch (architecture.md, "Bootstrap":
// `ichor adopt` "diff[s] each node's current config against a fresh render
// and stor[es] the difference as initial patches, so ownership transfers
// with zero drift").
//
// Only SysctlConfig and KernelModuleConfig documents are ever captured
// automatically. The legacy v1alpha1 Config document is deliberately never
// captured: TalosConfigPatchDocument's schema requires machine.token,
// machine.type and machine.certSANs on any Config document, machine.token
// mirrors the secrets bundle (the same material pkg/render's own leak test
// tracks), and there is no sparse way to patch only e.g. cluster.network
// without also restating it. Differences confined to that
// document come back as UnrepresentablePaths instead of a patch, and
// buildAdoptPlan's own zero-drift check (below) is what actually enforces
// this: it never returns a Patch unless applying it reproduces currentCfg
// byte for byte, so a legacy-document difference always surfaces as
// "nothing to do here automatically" rather than a silently incomplete
// patch.
func buildAdoptPlan(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	existing []ichorv1alpha1.TalosConfigPatch,
	currentCfg []byte,
) (*adoptPlan, error) {
	baseline, _, err := render.Render(cluster, m, bundle, existing)
	if err != nil {
		return nil, fmt.Errorf("adopt: rendering machine %q's baseline: %w", m.Name, err)
	}

	paths, err := controller.ChangedPaths(currentCfg, baseline)
	if err != nil {
		return nil, fmt.Errorf("adopt: diffing machine %q's current configuration: %w", m.Name, err)
	}

	plan := &adoptPlan{Machine: m.Name}

	if len(paths) == 0 {
		return plan, nil
	}

	wantSysctl, wantKernelModule, legacy := groupChangedPaths(paths)
	plan.UnrepresentablePaths = legacy

	docs, err := captureDocuments(currentCfg, wantSysctl, wantKernelModule)
	if err != nil {
		return nil, fmt.Errorf("adopt: capturing machine %q's current documents: %w", m.Name, err)
	}

	candidate := ichorv1alpha1.TalosConfigPatch{
		Name:      "adopt-" + m.Name,
		Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Layer:      "machine",
			Target:     &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: m.Name}},
			Documents:  docs,
		},
	}

	withCandidate := append(append([]ichorv1alpha1.TalosConfigPatch{}, existing...), candidate)

	rendered, _, err := render.Render(cluster, m, bundle, withCandidate)
	if err != nil {
		return nil, fmt.Errorf("adopt: verifying machine %q's zero-drift patch: %w", m.Name, err)
	}

	// Zero drift, enforced: this is the only path that returns a Patch, and
	// it never does so unless render(baseline + candidate) is byte-identical
	// to the node's real current configuration.
	if bytes.Equal(rendered, currentCfg) {
		plan.Patch = &candidate
	}

	return plan, nil
}

// groupChangedPaths partitions ChangedPaths' output by which
// TalosConfigPatchDocument kind each path belongs to, using diff.go's own
// rooting convention: a standalone document's paths are rooted at its kind
// name, everything else (the legacy Config document's "machine.*",
// "cluster.*" and top-level scalars) is not.
func groupChangedPaths(paths []string) (wantSysctl, wantKernelModule bool, legacy []string) {
	for _, p := range paths {
		kind := p
		if before, _, ok := strings.Cut(p, "."); ok {
			kind = before
		}

		switch kind {
		case kindSysctlConfig:
			wantSysctl = true
		case kindKernelModuleConfig:
			wantKernelModule = true
		default:
			legacy = append(legacy, p)
		}
	}

	return wantSysctl, wantKernelModule, legacy
}

// captureDocuments decodes currentCfg with machinery's own loader and
// converts every SysctlConfig/KernelModuleConfig document it finds (through their own generated FromMachinery
// conversions) into TalosConfigPatchDocuments. Neither kind carries secret
// material, so a whole-document capture is safe (unlike the legacy Config
// document; see buildAdoptPlan's doc comment).
func captureDocuments(currentCfg []byte, wantSysctl, wantKernelModule bool) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if !wantSysctl && !wantKernelModule {
		return nil, nil
	}

	provider, err := configloader.NewFromBytes(currentCfg)
	if err != nil {
		return nil, fmt.Errorf("decoding current configuration: %w", err)
	}

	var docs []ichorv1alpha1.TalosConfigPatchDocument

	for _, d := range provider.Documents() {
		switch v := d.(type) {
		case *mruntime.SysctlConfigV1Alpha1:
			if !wantSysctl {
				continue
			}

			conv, err := typedconfig.SysctlConfigV1Alpha1FromMachinery(v)
			if err != nil {
				return nil, fmt.Errorf("converting SysctlConfig: %w", err)
			}

			docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: kindSysctlConfig, SysctlConfig: conv})
		case *mruntime.KernelModuleConfigV1Alpha1:
			if !wantKernelModule {
				continue
			}

			conv, err := typedconfig.KernelModuleConfigV1Alpha1FromMachinery(v)
			if err != nil {
				return nil, fmt.Errorf("converting KernelModuleConfig: %w", err)
			}

			docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: kindKernelModuleConfig, KernelModuleConfig: conv})
		}
	}

	return docs, nil
}
