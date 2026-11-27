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
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	kindResolverConfig = "ResolverConfig"

	kindKubeAuthenticationConfig = "KubeAuthenticationConfig"

	// pathNetworkInterfaces is the v1alpha1 network block of a node whose Talos config predates the
	// multi-document form; spec.network renders the new form, so the block is captured as a patch.
	pathNetworkInterfaces = "machine.network.interfaces"

	// pathMachineCertSANs and pathAPIServerCertSANs are a node's own SAN lists. They are captured
	// into its patch when they differ from the cluster's, so no certificate changes (ADR 0143).
	pathMachineCertSANs   = "machine.certSANs"
	pathAPIServerCertSANs = "cluster.apiServer.certSANs"
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

	// IchorAuthenticator: the node carries Ichor's own authenticator, which adopt replaces by turning sign-in on.
	IchorAuthenticator bool
	// AuthenticatorRefused: adopt is not turning sign-in on, so the node's authenticator stays in the drift and adopt refuses the node.
	AuthenticatorRefused bool

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
	// IchorAuthenticator: this adopt turns sign-in on, so Ichor renders its own authenticator and the node's is not drift.
	IchorAuthenticator bool
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
// Only the documents of a shipped kind (ichorv1alpha1.TalosConfigPatchDocumentKinds) are ever
// captured automatically, whole, through their generated conversions. The v1alpha1 Config document is deliberately never
// captured: the mirror has no machine.token (secret material, which mirrorgen drops),
// machine.type and machine.certSANs are omitempty, and there is no sparse way to patch
// only e.g. cluster.network without also restating it. Differences confined to that
// document come back as UnrepresentablePaths instead of a patch, and
// buildAdoptPlan's own zero-drift check (below) is what actually enforces
// this: it never returns a Patch unless applying it reproduces currentCfg
// byte for byte, so a v1alpha1-document difference always surfaces as
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
	baseline, _, err := render.Render(cluster, m, bundle, existing, sys)
	if err != nil {
		return nil, fmt.Errorf("adopt: rendering node %q's baseline: %w", m.Name, err)
	}

	ichorAuth, err := refuseJWTAuthenticators(m.Name, currentCfg, baseline)
	if err != nil {
		return nil, err
	}

	paths, err := controller.ChangedPaths(currentCfg, baseline)
	if err != nil {
		return nil, fmt.Errorf("adopt: diffing node %q's current configuration: %w", m.Name, err)
	}

	// Ichor's own authenticator is not drift to capture when this adopt turns sign-in on and the render replaces it;
	// otherwise nothing renders it, and the patch keeps it.
	dropAuth := ichorAuth && own.IchorAuthenticator

	if dropAuth {
		paths = slices.DeleteFunc(paths, func(p string) bool { return underPath(p, kindKubeAuthenticationConfig) })
	}

	plan := &adoptPlan{Machine: m.Name, IchorAuthenticator: ichorAuth, AuthenticatorRefused: ichorAuth && !dropAuth}

	if len(paths) == 0 {
		return plan, nil
	}

	owned, rest := own.split(paths)
	plan.Owned = owned

	kinds, captured, unrepresentable := groupChangedPaths(rest)

	// spec.network.dnsServers already renders the ResolverConfig and the render refuses a patch that
	// carries one too (ADR 0138), so a difference there stays unrepresentable: set it in the spec.
	if kinds[kindResolverConfig] && m.Spec.Network != nil && len(m.Spec.Network.DNSServers) > 0 {
		delete(kinds, kindResolverConfig)

		for _, p := range rest {
			if strings.HasPrefix(p, kindResolverConfig+".") {
				unrepresentable = append(unrepresentable, p)
			}
		}
	}

	plan.UnrepresentablePaths = unrepresentable

	if len(kinds) == 0 && len(captured) == 0 {
		return plan, nil
	}

	docs, err := captureDocuments(currentCfg, kinds, captured, cluster.Spec.CertSANs)
	if err != nil {
		return nil, fmt.Errorf("adopt: capturing node %q's current documents: %w", m.Name, err)
	}

	config, err := ichorv1alpha1.ConfigFromDocuments(docs)
	if err != nil {
		return nil, fmt.Errorf("adopt: node %q's current documents: %w", m.Name, err)
	}

	candidate := ichorv1alpha1.TalosConfigPatch{
		Name:      "adopt-" + m.Name,
		Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Layer:      "machine",
			Target:     &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: m.Name}},
			Config:     config,
		},
	}

	withCandidate := append(append([]ichorv1alpha1.TalosConfigPatch{}, existing...), candidate)

	rendered, _, err := render.Render(cluster, m, bundle, withCandidate, sys)
	if err != nil {
		return nil, fmt.Errorf("adopt: verifying node %q's zero-drift patch: %w", m.Name, err)
	}

	// Zero drift, enforced: this is the only path that returns a Patch, and it never does so
	// unless render(baseline + candidate) differs from the node's real current configuration in
	// nothing but class B paths (with none, that is byte-identical).
	left, err := controller.ChangedPaths(currentCfg, rendered)
	if err != nil {
		return nil, fmt.Errorf("adopt: verifying node %q's zero-drift patch: %w", m.Name, err)
	}

	if dropAuth {
		left = slices.DeleteFunc(left, func(p string) bool { return underPath(p, kindKubeAuthenticationConfig) })
	}

	if _, notOwned := own.split(left); len(notOwned) == 0 {
		plan.Patch = &candidate
	} else {
		// The captured blocks did not reproduce the node (e.g. a WireGuard key the mirror drops, or a SAN
		// the cluster's list adds that a patch cannot take away): name what is still different, not
		// everything that was captured.
		for _, p := range notOwned {
			if !slices.Contains(plan.UnrepresentablePaths, p) {
				plan.UnrepresentablePaths = append(plan.UnrepresentablePaths, p)
			}
		}
	}

	return plan, nil
}

// refuseJWTAuthenticators refuses a machine whose KubeAuthenticationConfig carries a jwt entry
// the fresh render (baseline) does not: the render would replace it (the document is
// merge:replace), which would silently lock out whoever signs in through it. Refused, not
// dropped; the external provider belongs in spec.authentication.external. An issuer the cluster's own
// spec renders is not foreign, and neither is Ichor's own authenticator (isIchorJWT): adopt turns sign-in
// on again and the render replaces it with the new install's issuer and CA. own reports that last case.
func refuseJWTAuthenticators(machine string, currentCfg, baseline []byte) (own bool, err error) {
	have, err := jwtEntries(machine, currentCfg)
	if err != nil {
		return false, err
	}

	rendered, err := jwtEntries(machine, baseline)
	if err != nil {
		return false, err
	}

	foreign := 0

	for _, e := range have {
		switch {
		case slices.ContainsFunc(rendered, func(r jwtEntry) bool { return r.url == e.url }):
		case e.ichor:
			own = true
		default:
			foreign++
		}
	}

	if foreign > 0 {
		return false, fmt.Errorf("adopt: node %q has %d jwt authenticator(s) in its KubeAuthenticationConfig, which Ichor would replace "+
			"when it renders the node; move the identity provider into spec.authentication.external of the TalosCluster and adopt again",
			machine, foreign)
	}

	return own, nil
}

type jwtEntry struct {
	url   string
	ichor bool
}

// jwtEntries is every jwt authenticator in cfg's KubeAuthenticationConfig. An entry is Ichor's own when
// its only audience is render.IchorKubeAudience and both claim prefixes are render.IchorPrefix, which is
// what pkg/render/authentication.go renders for spec.authentication.ichor; an external provider cannot
// have that prefix (the CRD refuses it), so a user's own IdP is never taken for Ichor's.
func jwtEntries(machine string, cfg []byte) ([]jwtEntry, error) {
	provider, err := configloader.NewFromBytes(cfg)
	if err != nil {
		return nil, fmt.Errorf("adopt: decoding node %q's current configuration: %w", machine, err)
	}

	auth := provider.K8sAuthenticationConfig()
	if auth == nil {
		return nil, nil
	}

	var entries []jwtEntry

	jwt, _ := auth.Configuration()["jwt"].([]any)
	for _, j := range jwt {
		jm, _ := j.(map[string]any)
		issuer, _ := jm["issuer"].(map[string]any)
		url, _ := issuer["url"].(string)
		audiences, _ := issuer["audiences"].([]any)
		mappings, _ := jm["claimMappings"].(map[string]any)

		entries = append(entries, jwtEntry{url: url, ichor: len(audiences) == 1 && audiences[0] == render.IchorKubeAudience &&
			claimPrefix(mappings, "username") == render.IchorPrefix && claimPrefix(mappings, "groups") == render.IchorPrefix})
	}

	return entries, nil
}

func claimPrefix(mappings map[string]any, which string) string {
	m, _ := mappings[which].(map[string]any)
	prefix, _ := m["prefix"].(string)

	return prefix
}

// groupChangedPaths partitions ChangedPaths' output by which
// TalosConfigPatchDocument kind each path belongs to, using diff.go's own
// rooting convention: a standalone document's paths are rooted at its kind
// name, everything else (the Config document's "machine.*",
// "cluster.*" and top-level scalars) is not. A kind is any the generated
// TalosConfigPatchDocumentKinds lists, so a new shipped kind needs no edit here.
func groupChangedPaths(paths []string) (kinds map[string]bool, captured, unrepresentable []string) {
	kinds = map[string]bool{}
	shipped := ichorv1alpha1.TalosConfigPatchDocumentKinds()

	for _, p := range paths {
		if slices.Contains([]string{pathNetworkInterfaces, pathMachineCertSANs, pathAPIServerCertSANs}, p) {
			captured = append(captured, p)

			continue
		}

		kind, _, _ := strings.Cut(p, ".")
		if slices.Contains(shipped, kind) {
			kinds[kind] = true
		} else {
			unrepresentable = append(unrepresentable, p)
		}
	}

	return kinds, captured, unrepresentable
}

// sansBeyond are the node's SANs that spec.certSANs does not render: a patch's list is appended to
// the rendered one, never merged into it.
func sansBeyond(node, spec []string) []string {
	return slices.DeleteFunc(slices.Clone(node), func(s string) bool { return slices.Contains(spec, s) })
}

// captureDocuments decodes currentCfg with machinery's own loader and converts every document of
// a wanted kind it finds, through the generated TalosConfigPatchDocumentFromMachinery, into a
// TalosConfigPatchDocument. The mirror types drop secret material (ADR 0138), so a whole-document
// capture is safe (unlike the v1alpha1 Config document; see buildAdoptPlan's doc comment), and the
// zero-drift check names whatever a mirror dropped.
func captureDocuments(currentCfg []byte, kinds map[string]bool, captured []string, specSANs []string) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if len(kinds) == 0 && len(captured) == 0 {
		return nil, nil
	}

	provider, err := configloader.NewFromBytes(currentCfg)
	if err != nil {
		return nil, fmt.Errorf("decoding current configuration: %w", err)
	}

	var docs []ichorv1alpha1.TalosConfigPatchDocument

	// Only the captured subtrees of the Config document (interfaces, certSANs), never the
	// document: it carries the machine and cluster secrets.
	raw := provider.RawV1Alpha1() //nolint:staticcheck // the v1alpha1 block is what the node runs
	if len(captured) > 0 && raw != nil && raw.MachineConfig != nil {
		sub := &mv1alpha1.Config{ConfigVersion: mv1alpha1.Version, MachineConfig: &mv1alpha1.MachineConfig{}}

		//nolint:staticcheck // the v1alpha1 block is what the node runs
		if slices.Contains(captured, pathNetworkInterfaces) && raw.MachineConfig.MachineNetwork != nil {
			sub.MachineConfig.MachineNetwork = &mv1alpha1.NetworkConfig{NetworkInterfaces: raw.MachineConfig.MachineNetwork.NetworkInterfaces}
		}

		if slices.Contains(captured, pathMachineCertSANs) {
			sub.MachineConfig.MachineCertSANs = sansBeyond(raw.MachineConfig.MachineCertSANs, specSANs)
		}

		conv, err := typedconfig.ConfigFromMachinery(sub)
		if err != nil {
			return nil, fmt.Errorf("converting %s: %w", strings.Join(captured, ", "), err)
		}

		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: "Config", Config: conv})
	}

	// The API server's own SANs are the KubeAPIServerConfig document's setting in Ichor's schema.
	//nolint:staticcheck // the v1alpha1 block is what the node runs
	if slices.Contains(captured, pathAPIServerCertSANs) && !kinds["KubeAPIServerConfig"] && raw != nil && raw.ClusterConfig != nil && raw.ClusterConfig.APIServerConfig != nil {
		docs = append(docs, ichorv1alpha1.TalosConfigPatchDocument{Kind: "KubeAPIServerConfig", KubeAPIServerConfig: &typedconfig.KubeAPIServerConfigV1Alpha1{
			PodCertExtraSANs: sansBeyond(raw.ClusterConfig.APIServerConfig.ExtraCertSANs, specSANs),
		}})
	}

	for _, d := range provider.Documents() {
		if !kinds[d.Kind()] {
			continue
		}

		conv, err := ichorv1alpha1.TalosConfigPatchDocumentFromMachinery(d)
		if err != nil {
			return nil, fmt.Errorf("converting %s: %w", d.Kind(), err)
		}

		docs = append(docs, conv)
	}

	return docs, nil
}

// contractProbe is the outcome of rendering one machine at one version contract and diffing it
// against the live configuration.
type contractProbe struct {
	Contract string
	// UnrepresentablePaths are the class C paths: changed paths outside every
	// document of a shipped kind.
	UnrepresentablePaths []string
	// Err is why the render at this contract failed; such a contract is never chosen.
	Err error
}

// probeVersionContract renders m at each contract from the nodes' running Talos version down to
// v1.12 (contractCandidates) and returns the probes plus the chosen contract: the one with the
// fewest unrepresentable paths, the higher contract on a tie. A cluster generated
// on an old talosctl and upgraded in place has a v1alpha1-form configuration, so forcing the
// running version's contract would turn every field into drift.
//
// It never mutates cluster. It returns an error if no candidate renders, including when running is
// below the v1.14 node floor.
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
				_, _, probe.UnrepresentablePaths = groupChangedPaths(paths)
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
		return "", probes, fmt.Errorf("adopt: node %q renders at none of the version contracts %s: %w",
			m.Name, strings.Join(candidates, ", "), probes[0].Err)
	}

	return probes[best].Contract, probes, nil
}
