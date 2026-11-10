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
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	mnetwork "github.com/siderolabs/talos/pkg/machinery/config/types/network"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	kindSysctlConfig       = "SysctlConfig"
	kindKernelModuleConfig = "KernelModuleConfig"
	kindResolverConfig     = "ResolverConfig"

	// pathNetworkInterfaces is the legacy network block of a node whose Talos config predates the
	// multi-document form; spec.network renders the new form, so the block is captured as a patch.
	pathNetworkInterfaces = "machine.network.interfaces"
)

// adoptPlan is what buildAdoptPlan found for one machine: the
// TalosConfigPatch that closes the gap between its live configuration and
// a fresh render (nil if there was no gap, or if the gap could not be
// closed safely), plus the field paths -- names only, never values -- that
// this command left untouched either way.
type adoptPlan struct {
	Machine string
	Patch   *ichorv1alpha1.TalosConfigPatch
	// UnrepresentablePaths are the class C paths: they block the machine.
	UnrepresentablePaths []string
	// Owned are the class B paths: Ichor-owned settings the first render will change. They are
	// listed for consent and do not block.
	Owned []string

	// Skipped, when set, is why adopt did not look at this machine at all.
	Skipped string
}

// ownership says which config paths Ichor owns on this cluster (class B). The zero
// value owns nothing, which is what an unmanaged cluster gets: no CNI item is ever class B.
type ownership struct {
	// Managed is true for the cilium preset.
	Managed bool
	// KubeProxyReplaced makes the kube-proxy paths Ichor's.
	KubeProxyReplaced bool
	// CiliumInline makes the inline-manifest paths Ichor's (the node carries a Cilium inline entry).
	CiliumInline bool
}

var (
	ownedCNIPrefixes      = []string{"cluster.network.cni", "KubeFlannelCNIConfig"}
	ownedKubePrismPrefix  = []string{"machine.features.kubePrism", "KubePrismConfig"}
	ownedKubeProxyPrefix  = []string{"cluster.proxy", "KubeProxyConfig"}
	ownedInlineManifestPf = []string{"cluster.inlineManifests", "KubeInlineManifestConfig"}
)

// underPath reports whether p is pf or a path below it.
func underPath(p, pf string) bool {
	return p == pf || strings.HasPrefix(p, pf+".") || strings.HasPrefix(p, pf+"[")
}

func hasPathPrefix(p string, prefixes []string) bool {
	return slices.ContainsFunc(prefixes, func(pf string) bool { return underPath(p, pf) })
}

// owned reports whether path is a class B path.
func (o ownership) owned(path string) bool {
	if !o.Managed {
		return false
	}

	return hasPathPrefix(path, ownedCNIPrefixes) || hasPathPrefix(path, ownedKubePrismPrefix) ||
		(o.KubeProxyReplaced && hasPathPrefix(path, ownedKubeProxyPrefix)) ||
		(o.CiliumInline && hasPathPrefix(path, ownedInlineManifestPf))
}

// splitOwned separates class B paths from the rest.
func (o ownership) split(paths []string) (owned, rest []string) {
	for _, p := range paths {
		if o.owned(p) {
			owned = append(owned, p)
		} else {
			rest = append(rest, p)
		}
	}

	return owned, rest
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
// captured: the mirror has no machine.token (secret material, which mirrorgen drops),
// machine.type and machine.certSANs are omitempty, and there is no sparse way to patch
// only e.g. cluster.network without also restating it. Differences confined to that
// document come back as UnrepresentablePaths instead of a patch, and
// buildAdoptPlan's own zero-drift check (below) is what actually enforces
// this: it never returns a Patch unless applying it reproduces currentCfg
// byte for byte, so a legacy-document difference always surfaces as
// "nothing to do here automatically" rather than a silently incomplete
// patch.
//
// The drift is sorted into the three classes, 1.7: A (captured as a patch), B (own,
// listed) and C (unrepresentable, blocks). A patch is returned only when nothing but class B
// remains once it is applied.
func buildAdoptPlan(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	existing []ichorv1alpha1.TalosConfigPatch,
	currentCfg []byte,
	sys *render.SystemInputs,
	own ownership,
) (*adoptPlan, error) {
	if err := refuseJWTAuthenticators(m.Name, currentCfg); err != nil {
		return nil, err
	}

	baseline, _, err := render.Render(cluster, m, bundle, existing, sys)
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

	owned, rest := own.split(paths)
	plan.Owned = owned

	wantSysctl, wantKernelModule, wantInterfaces, wantResolver, legacy := groupChangedPaths(rest)

	// spec.network.dnsServers already renders the ResolverConfig and the render refuses a patch that
	// carries one too (ADR 0138), so a difference there stays unrepresentable: set it in the spec.
	if wantResolver && m.Spec.Network != nil && len(m.Spec.Network.DNSServers) > 0 {
		wantResolver = false

		for _, p := range rest {
			if strings.HasPrefix(p, kindResolverConfig+".") {
				legacy = append(legacy, p)
			}
		}
	}

	plan.UnrepresentablePaths = legacy

	if !wantSysctl && !wantKernelModule && !wantInterfaces && !wantResolver {
		return plan, nil
	}

	docs, err := captureDocuments(currentCfg, wantSysctl, wantKernelModule, wantInterfaces, wantResolver)
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

	rendered, _, err := render.Render(cluster, m, bundle, withCandidate, sys)
	if err != nil {
		return nil, fmt.Errorf("adopt: verifying machine %q's zero-drift patch: %w", m.Name, err)
	}

	// Zero drift, enforced: this is the only path that returns a Patch, and it never does so
	// unless render(baseline + candidate) differs from the node's real current configuration in
	// nothing but class B paths (with none, that is byte-identical).
	left, err := controller.ChangedPaths(currentCfg, rendered)
	if err != nil {
		return nil, fmt.Errorf("adopt: verifying machine %q's zero-drift patch: %w", m.Name, err)
	}

	if _, notOwned := own.split(left); len(notOwned) == 0 {
		plan.Patch = &candidate
	} else if wantInterfaces {
		// The captured block did not reproduce the node (e.g. a WireGuard key the mirror drops).
		plan.UnrepresentablePaths = append(plan.UnrepresentablePaths, pathNetworkInterfaces)
	}

	return plan, nil
}

// refuseJWTAuthenticators refuses a machine whose KubeAuthenticationConfig already carries jwt
// entries: the render would replace them (the document is merge:replace), which would
// silently lock out whoever signs in through them. Refused, not dropped; the external provider
// belongs in spec.authentication.external.
func refuseJWTAuthenticators(machine string, currentCfg []byte) error {
	provider, err := configloader.NewFromBytes(currentCfg)
	if err != nil {
		return fmt.Errorf("adopt: decoding machine %q's current configuration: %w", machine, err)
	}

	auth := provider.K8sAuthenticationConfig()
	if auth == nil {
		return nil
	}

	if jwt, _ := auth.Configuration()["jwt"].([]any); len(jwt) > 0 {
		return fmt.Errorf("adopt: machine %q has %d jwt authenticator(s) in its KubeAuthenticationConfig, which Ichor would replace "+
			"when it renders the node; move the identity provider into spec.authentication.external of the TalosCluster and adopt again",
			machine, len(jwt))
	}

	return nil
}

// groupChangedPaths partitions ChangedPaths' output by which
// TalosConfigPatchDocument kind each path belongs to, using diff.go's own
// rooting convention: a standalone document's paths are rooted at its kind
// name, everything else (the legacy Config document's "machine.*",
// "cluster.*" and top-level scalars) is not.
func groupChangedPaths(paths []string) (wantSysctl, wantKernelModule, wantInterfaces, wantResolver bool, legacy []string) {
	for _, p := range paths {
		if p == pathNetworkInterfaces {
			wantInterfaces = true

			continue
		}

		kind := p
		if before, _, ok := strings.Cut(p, "."); ok {
			kind = before
		}

		switch kind {
		case kindSysctlConfig:
			wantSysctl = true
		case kindKernelModuleConfig:
			wantKernelModule = true
		case kindResolverConfig:
			wantResolver = true
		default:
			legacy = append(legacy, p)
		}
	}

	return wantSysctl, wantKernelModule, wantInterfaces, wantResolver, legacy
}

// captureDocuments decodes currentCfg with machinery's own loader and
// converts every SysctlConfig/KernelModuleConfig document it finds (through their own generated FromMachinery
// conversions) into TalosConfigPatchDocuments. Neither kind carries secret
// material, so a whole-document capture is safe (unlike the legacy Config
// document; see buildAdoptPlan's doc comment).
func captureDocuments(currentCfg []byte, wantSysctl, wantKernelModule, wantInterfaces, wantResolver bool) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if !wantSysctl && !wantKernelModule && !wantInterfaces && !wantResolver {
		return nil, nil
	}

	provider, err := configloader.NewFromBytes(currentCfg)
	if err != nil {
		return nil, fmt.Errorf("decoding current configuration: %w", err)
	}

	var docs []ichorv1alpha1.TalosConfigPatchDocument

	// Only the interfaces subtree of the legacy Config document, never the document: it carries
	// the machine and cluster secrets.
	//nolint:staticcheck // the legacy block is what the node runs
	if raw := provider.RawV1Alpha1(); wantInterfaces && raw != nil && raw.MachineConfig != nil && raw.MachineConfig.MachineNetwork != nil {
		conv, err := typedconfig.ConfigFromMachinery(&mv1alpha1.Config{
			ConfigVersion: mv1alpha1.Version,
			MachineConfig: &mv1alpha1.MachineConfig{MachineNetwork: &mv1alpha1.NetworkConfig{
				NetworkInterfaces: raw.MachineConfig.MachineNetwork.NetworkInterfaces,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("converting machine.network.interfaces: %w", err)
		}

		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: "Config", Config: conv})
	}

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
		case *mnetwork.ResolverConfigV1Alpha1:
			if !wantResolver {
				continue
			}

			conv, err := typedconfig.ResolverConfigV1Alpha1FromMachinery(v)
			if err != nil {
				return nil, fmt.Errorf("converting ResolverConfig: %w", err)
			}

			docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: kindResolverConfig, ResolverConfig: conv})
		}
	}

	return docs, nil
}

// contractProbe is the outcome of rendering one machine at one version contract and diffing it
// against the live configuration.
type contractProbe struct {
	Contract string
	// UnrepresentablePaths are the class C paths: changed paths that are neither
	// a SysctlConfig nor a KernelModuleConfig difference.
	UnrepresentablePaths []string
	// Err is why the render at this contract failed; such a contract is never chosen.
	Err error
}

// probeVersionContract renders m at each contract from the nodes' running Talos version down to
// v1.12 (contractCandidates) and returns the probes plus the chosen contract: the one with the
// fewest unrepresentable paths, the higher contract on a tie. A cluster generated
// on an old talosctl and upgraded in place has a legacy-form configuration, so forcing the
// running version's contract would turn every field into drift.
//
// It never mutates cluster. It returns an error if no candidate renders, including when running is
// below the v1.12 floor.
func probeVersionContract(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	existing []ichorv1alpha1.TalosConfigPatch,
	currentCfg []byte,
	sys *render.SystemInputs,
	running string,
) (string, []contractProbe, error) {
	candidates := contractCandidates(running)
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("adopt: no version contract to probe for Talos %q (the floor is %s)", running, adoptMinTalos)
	}

	probes := make([]contractProbe, 0, len(candidates))
	best := -1

	for _, c := range candidates {
		trial := cluster.DeepCopy()
		trial.Spec.VersionContract = c

		probe := contractProbe{Contract: c}

		baseline, _, err := render.Render(trial, m, bundle, existing, sys)
		if err == nil {
			var paths []string

			paths, err = controller.ChangedPaths(currentCfg, baseline)
			if err == nil {
				_, _, _, _, probe.UnrepresentablePaths = groupChangedPaths(paths)
			}
		}

		probe.Err = err
		probes = append(probes, probe)

		// candidates run highest first, so a strict less-than keeps the higher contract on a tie.
		if err == nil && (best < 0 || len(probe.UnrepresentablePaths) < len(probes[best].UnrepresentablePaths)) {
			best = len(probes) - 1
		}
	}

	if best < 0 {
		return "", probes, fmt.Errorf("adopt: machine %q renders at none of the version contracts %s: %w",
			m.Name, strings.Join(candidates, ", "), probes[0].Err)
	}

	return probes[best].Contract, probes, nil
}
