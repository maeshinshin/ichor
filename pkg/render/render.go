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
//
// One function is the deliberate exception: ResolveRegistryAuth (registry.go,
// ADR 0027) reads a Secret through a Kubernetes client, the same role
// internal/controller's loadBundle plays for the secrets bundle. It still
// touches no Talos API and no node; what it returns is handed to
// Render/BuildConfig exactly the way an already-loaded secrets.Bundle is.
package render

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"reflect"
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
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// ConditionCNIHandover is the TalosCluster.status condition type the CNI
// hand-over reconciler (internal/controller, ADR 0024) writes once Argo CD
// reports the CNI's Application Synced and Healthy. Render reads it
// directly, rather than taking a new parameter every caller would have to
// thread through, to decide whether to keep rendering the CNI's
// cluster.inlineManifests entry (see dropHandedOverInlineManifest):
// Talos's own ManifestApplyController re-applies inlineManifests during a
// Kubernetes upgrade, so a stale entry would roll the CNI back to
// whatever Ichor originally inlined it as.
const ConditionCNIHandover = "CNIHandover"

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
//
// sys carries the system layer's cluster-read inputs (ADR 0027, ADR 0050): resolved registry
// credentials and the TalosStorages. Production callers build it with ResolveSystemInputs. It is
// a required argument rather than a second entry point, so a caller cannot forget it; nil is
// right for a cluster with neither registries nor storage.
func Render(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	candidates []ichorv1alpha1.TalosConfigPatch,
	sys *SystemInputs,
) ([]byte, string, error) {
	cfg, err := BuildConfig(cluster, m, bundle, candidates, sys)
	if err != nil {
		return nil, "", err
	}

	out, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		return nil, "", fmt.Errorf("render: encoding rendered config: %w", err)
	}

	return out, Hash(out), nil
}

// Hash returns data's hash in the "sha256:<hex>" format Render's own hash,
// TalosMachineReconciler's desiredInputsHash and hashConfig all use, so a
// config and whatever it's compared against are hashed identically no
// matter which of those three call sites produced either side.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)

	return fmt.Sprintf("sha256:%x", sum)
}

// BuildConfig runs every step of Render up to (and including) validation,
// but stops short of encoding: it returns the validated coreconfig.Provider
// itself rather than bytes. Render is the only caller that needs the final
// bytes and hash; a caller that needs the Provider instead -- the UI
// backend's effective-config view, which calls Provider.RedactSecrets
// before encoding so a secret is never serialized unmasked (ADR 0016, ADR
// 0018) -- calls BuildConfig directly instead of re-deriving the base
// config, patch selection and merge Render already does.
//
// cluster.Spec.Registries' mirror endpoints and the resolved credentials in
// sys are merged in as the system layer (ADR 0027): they land below every
// TalosConfigPatch layer, so a cluster, role, selector or machine patch that
// also touches machine.registries can still override them. A host with no
// entry in auth renders as an anonymous mirror.
func BuildConfig(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle,
	candidates []ichorv1alpha1.TalosConfigPatch,
	sys *SystemInputs,
) (coreconfig.Provider, error) {
	if bundle == nil {
		return nil, fmt.Errorf("render: secrets bundle is required")
	}

	if err := checkCNISupported(cluster); err != nil {
		return nil, err
	}

	if sys == nil {
		sys = &SystemInputs{}
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return nil, fmt.Errorf("render: parsing versionContract: %w", err)
	}

	// machine.ParseType treats "" as TypeWorker, not an error -- it exists
	// to parse config documents, where an absent machineType field has
	// always meant worker. TalosMachineSpec.Role is different: it is left
	// empty by discovery before approval assigns one (ADR 0011), and an
	// approved machine with no role must never silently render as a worker.
	// Checked explicitly, ahead of ParseType, for exactly that reason.
	if m.Spec.Role == "" {
		return nil, fmt.Errorf("render: machine role is not set")
	}

	machineType, err := machine.ParseType(m.Spec.Role)
	if err != nil {
		return nil, fmt.Errorf("render: parsing machine role: %w", err)
	}

	// A CA rotation in progress changes which CA the base configuration is generated from and
	// which CAs are accepted besides it (ADR 0063).
	baseBundle, accepted, err := sys.CARotation.apply(bundle)
	if err != nil {
		return nil, err
	}

	cfg, err := baseConfig(cluster, baseBundle, contract, machineType)
	if err != nil {
		return nil, err
	}

	// The accepted CAs of a CA rotation and the new keys of a key rotation (which sit next to the old ones in
	// the control planes' documents, ADR 0121) go into the generated configuration.
	cfg, err = injectRotations(cfg, accepted, sys.KeyRotation, bundle, machineType)
	if err != nil {
		return nil, err
	}

	// System layer (ADR 0027): registry mirrors and their resolved auth are
	// merged in before any TalosConfigPatch, same as the control-plane
	// taint above -- Ichor-generated, not patch-shaped, and the lowest
	// layer in the bottom-up stack so a cluster/role/selector/machine patch
	// that also sets machine.registries still wins.
	cfg, err = injectRegistries(cfg, contract, cluster, sys.RegistryAuth)
	if err != nil {
		return nil, err
	}

	// The CNI Talos runs and the install target (ADR 0077) are system layer too.
	// An unmanaged cluster keeps its own CNI: nothing is emitted unless its config selects none.
	if cluster.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged || cniSelectionNone(cluster) {
		cfg, err = injectCNI(cfg, contract)
		if err != nil {
			return nil, err
		}
	}

	cfg, err = injectInstall(cfg, contract, m)
	if err != nil {
		return nil, err
	}

	// Approval-time network and disk roles (ADR 0092).
	cfg, err = injectNetwork(cfg, contract, m)
	if err != nil {
		return nil, err
	}

	cfg, err = injectDisks(cfg, m)
	if err != nil {
		return nil, err
	}

	// Storage preparation (layer 1, ADR 0050) is system layer too, above the registries and
	// below every patch.
	cfg, err = injectStorage(cfg, contract, cluster, m, sys)
	if err != nil {
		return nil, err
	}

	// Control-plane metrics exposure and its source restriction (ADR 0069): system layer, so a
	// patch can still narrow what it renders.
	cfg, err = injectControlPlaneMetrics(cfg, contract, cluster, machineType)
	if err != nil {
		return nil, err
	}

	// Opt-in hardening (ADR 0108), below every patch.
	cfg, err = injectSecurity(cfg, contract, cluster, machineType)
	if err != nil {
		return nil, err
	}

	// Who the kube-apiserver trusts to sign users in (ADR 0094): the typed KubeAuthenticationConfig.
	cfg, err = injectAuthentication(cfg, contract, cluster, machineType, sys)
	if err != nil {
		return nil, err
	}

	// spec.nodeLabels is the machine's own intent, applied over the storage preset's labels.
	cfg, err = injectNodeLabels(cfg, contract, m)
	if err != nil {
		return nil, err
	}

	// A staged Kubernetes upgrade pins the kubelet and kube-proxy below the spec (ADR 0087).
	cfg, err = injectStagedVersions(cfg, contract, sys.Staged)
	if err != nil {
		return nil, err
	}

	ordered, err := SelectPatches(cluster, m, candidates)
	if err != nil {
		return nil, err
	}

	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, ConditionCNIHandover) {
		ordered = dropHandedOverInlineManifest(ordered, takenOverInlineManifest(cluster))
	}

	// The Talos side of the kube-proxy split is Ichor's, not a patch's (ADR 0066).
	if kubeProxyOwned(cluster) {
		ordered = dropProxyDisabled(ordered)
	}

	// Talos allows kubernetesTalosAPIAccess on control planes only: a cluster-wide patch that
	// enables it applies there and is left out of workers.
	ordered = dropTalosAPIAccess(ordered, m)

	cfg, err = applyPatches(cfg, contract, ordered)
	if err != nil {
		return nil, fmt.Errorf("render: applying patches: %w", err)
	}

	cfg, err = injectHostname(cfg, contract, m)
	if err != nil {
		return nil, err
	}

	cfg, err = injectKubeProxy(cfg, contract, cluster)
	if err != nil {
		return nil, err
	}

	if _, err := cfg.ValidateAsClient(clientRuntimeMode{}); err != nil {
		return nil, fmt.Errorf("render: validating rendered config: %w", err)
	}

	return cfg, nil
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
	schedulable := cluster.Spec.ControlPlane.Schedulable
	if schedulable != "true" && schedulable != "false" {
		return nil, fmt.Errorf("render: controlPlane.schedulable %q is invalid: must be %q or %q",
			schedulable, "true", "false")
	}

	endpoint := fmt.Sprintf("https://%s:%d", cluster.Spec.Endpoint.Address, constants.DefaultControlPlanePort)

	// spec.kubernetesVersion is "vX.Y.Z" (the CRD pattern), but machinery's generator takes it
	// without the "v" and adds one itself when it builds the component images; passed through
	// as-is it renders "kube-apiserver:vv1.36.0", an image that does not exist.
	input, err := generate.NewInput(cluster.Name, endpoint, strings.TrimPrefix(cluster.Spec.KubernetesVersion, "v"),
		generate.WithVersionContract(contract),
		generate.WithSecretsBundle(bundle),
		generate.WithAllowSchedulingOnControlPlanes(schedulable == "true"),
		generate.WithAdditionalSubjectAltNames(cluster.Spec.CertSANs),
	)
	if err != nil {
		return nil, fmt.Errorf("render: preparing generator input: %w", err)
	}

	// spec.network (ADR 0077): the generator renders these as the KubeNetworkConfig document from
	// the v1.14 contract and as cluster.network before it, so it, not Ichor, picks the form.
	if n := cluster.Spec.Network; n != nil {
		if len(n.PodSubnets) > 0 {
			input.PodNet = n.PodSubnets
		}

		if len(n.ServiceSubnets) > 0 {
			input.ServiceNet = n.ServiceSubnets
		}
	}

	cfg, err := input.Config(machineType)
	if err != nil {
		return nil, fmt.Errorf("render: generating base config: %w", err)
	}

	return cfg, nil
}

// SelectPatches implements Configuration reconcile step 3 (which patches
// apply to this machine) and the layer/priority half of step 2: it filters
// candidates down to the ones that apply to m, then returns them in the
// order they must be applied -- bottom-up by layer, then by priority within
// a layer, then by name as a stable tie-break so that the result does not
// depend on the order candidates arrived in.
//
// Exported so callers that need to know exactly which inputs fed a render
// (the drift check in internal/controller) can ask the same question
// Render itself answers, instead of re-deriving patch applicability.
func SelectPatches(
	cluster *ichorv1alpha1.TalosCluster,
	m *ichorv1alpha1.TalosMachine,
	candidates []ichorv1alpha1.TalosConfigPatch,
) ([]*ichorv1alpha1.TalosConfigPatch, error) {
	ordered := make([]*ichorv1alpha1.TalosConfigPatch, 0, len(candidates))

	for i := range candidates {
		p := &candidates[i]

		ok, err := patchApplies(cluster, m, p)
		if err != nil {
			return nil, err
		}

		if ok {
			ordered = append(ordered, p)
		}
	}

	slices.SortFunc(ordered, func(a, b *ichorv1alpha1.TalosConfigPatch) int {
		return cmp.Or(cmp.Compare(layerOrder[a.Spec.Layer], layerOrder[b.Spec.Layer]),
			cmp.Compare(a.Spec.Priority, b.Spec.Priority), cmp.Compare(a.Name, b.Name))
	})

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

// takenOverInlineManifest is the name of the adopted cluster's own Cilium inline manifest entry
// (ADR 0103), recorded in status once the hand-over has completed; "" for a cluster Ichor
// bootstrapped.
func takenOverInlineManifest(cluster *ichorv1alpha1.TalosCluster) string {
	if cluster.Status.CNI == nil {
		return ""
	}

	return cluster.Status.CNI.TakenOverInlineManifest
}

// dropHandedOverInlineManifest returns ordered with the CNI's
// cni.InlineManifestName entry, and the entry named taken (an adopted cluster's own), removed from
// every Config document's ClusterConfig.ClusterInlineManifests. It never mutates the caller's own
// TalosConfigPatch objects (principle 5: the controller renders from
// ingredients, it does not rewrite them) -- a patch is only ever
// deep-copied if it actually carries the entry; every other patch passes
// through as the same pointer.
func dropHandedOverInlineManifest(
	ordered []*ichorv1alpha1.TalosConfigPatch, taken string,
) []*ichorv1alpha1.TalosConfigPatch {
	isHandedOver := func(e *typedconfig.ClusterInlineManifest) bool {
		return e.InlineManifestName == cni.InlineManifestName || (taken != "" && e.InlineManifestName == taken)
	}

	return rewriteConfigs(ordered,
		func(c *typedconfig.Config) bool {
			return c.ClusterConfig != nil && slices.ContainsFunc(c.ClusterConfig.ClusterInlineManifests, isHandedOver)
		},
		func(c *typedconfig.Config) {
			// c is rewriteConfigs's deep copy, so the caller's patch keeps its entries.
			c.ClusterConfig.ClusterInlineManifests = slices.DeleteFunc(c.ClusterConfig.ClusterInlineManifests, isHandedOver)
		})
}

// dropTalosAPIAccess, for a machine that is not a control plane, returns ordered without
// machine.features.kubernetesTalosAPIAccess, deep-copying only the patches that set it.
func dropTalosAPIAccess(
	ordered []*ichorv1alpha1.TalosConfigPatch, m *ichorv1alpha1.TalosMachine,
) []*ichorv1alpha1.TalosConfigPatch {
	if m.Spec.Role == "controlplane" {
		return ordered
	}

	return rewriteConfigs(ordered,
		func(c *typedconfig.Config) bool {
			return c.MachineConfig != nil && c.MachineConfig.MachineFeatures != nil &&
				c.MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig != nil
		},
		func(c *typedconfig.Config) { c.MachineConfig.MachineFeatures.KubernetesTalosAPIAccessConfig = nil })
}

// rewriteConfigs returns ordered with edit applied to every Config document for which needs is
// true. It deep-copies only the patches it edits; every other patch passes through as the same
// pointer, and the caller's own objects are never mutated.
func rewriteConfigs(
	ordered []*ichorv1alpha1.TalosConfigPatch,
	needs func(*typedconfig.Config) bool, edit func(*typedconfig.Config),
) []*ichorv1alpha1.TalosConfigPatch {
	out := make([]*ichorv1alpha1.TalosConfigPatch, len(ordered))

	for i, p := range ordered {
		out[i] = p

		for di, doc := range p.Spec.Documents {
			if doc.Config == nil || !needs(doc.Config) {
				continue
			}

			if out[i] == p {
				out[i] = p.DeepCopy()
			}

			edit(out[i].Spec.Documents[di].Config)
		}
	}

	return out
}

// applyPatches merges each patch's documents onto cfg in the given order,
// one patch at a time, so that a later patch's non-zero fields win over an
// earlier one's (Configuration reconcile step 2).
func applyPatches(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, ordered []*ichorv1alpha1.TalosConfigPatch,
) (coreconfig.Provider, error) {
	patches := make([]configpatcher.Patch, 0, len(ordered))

	for _, p := range ordered {
		docs := make([]config.Document, 0, len(p.Spec.Documents))

		for _, d := range p.Spec.Documents {
			doc, err := DocumentFromPatch(d)
			if err != nil {
				return nil, fmt.Errorf("render: patch %q: %w", p.Name, err)
			}

			docs = append(docs, doc)

			if contract.MultidocKubernetesConfigSupported() {
				docs = append(docs, takeInlineManifests(doc)...)
			}
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

// takeInlineManifests moves the cluster.inlineManifests entries of a freshly converted legacy
// Config document out into KubeInlineManifestConfig documents, the v1.14 form (the legacy field
// is deprecated there). Patches can only carry the legacy field, so this is the one place a
// v1.14 contract differs. doc is the caller's own conversion result, never a shared object.
//
//nolint:staticcheck // reads the deprecated legacy field on purpose: it is what is being converted
func takeInlineManifests(doc config.Document) []config.Document {
	c, ok := doc.(*mv1alpha1.Config) //nolint:staticcheck // the legacy block is what patches carry
	if !ok || c.ClusterConfig == nil || len(c.ClusterConfig.ClusterInlineManifests) == 0 {
		return nil
	}

	out := make([]config.Document, 0, len(c.ClusterConfig.ClusterInlineManifests))

	for _, m := range c.ClusterConfig.ClusterInlineManifests {
		d := k8s.NewKubeInlineManifestConfigV1Alpha1()
		d.MetaName = m.InlineManifestName
		d.ManifestSpec = m.InlineManifestContents
		out = append(out, d)
	}

	c.ClusterConfig.ClusterInlineManifests = nil

	if reflect.DeepEqual(*c.ClusterConfig, mv1alpha1.ClusterConfig{}) {
		c.ClusterConfig = nil
	}

	return out
}

// mergeDocument strategic-merges docs into cfg as one patch, the way every system-layer step
// does; what names the step in an error.
func mergeDocument(cfg coreconfig.Provider, what string, docs ...config.Document) (coreconfig.Provider, error) {
	c, err := container.New(docs...)
	if err != nil {
		return nil, fmt.Errorf("render: %s: %w", what, err)
	}

	out, err := configpatcher.Apply(configpatcher.WithConfig(cfg),
		[]configpatcher.Patch{configpatcher.NewStrategicMergePatch(c)})
	if err != nil {
		return nil, fmt.Errorf("render: applying %s: %w", what, err)
	}

	return out.Config()
}

// DocumentFromPatch converts one TalosConfigPatchDocument to the machinery
// document its kind selects, through the typedconfig mirror type's own
// ToMachinery conversion (ADR 0003). The CRD's discriminator validation
// guarantees exactly one of the fields below is populated and that it
// matches Kind; the default case only guards against a value that bypassed
// that validation (e.g. in a unit test).
//
// Exported so the UI backend's effective-config view (ADR 0018) can encode
// one patch's own documents in isolation -- to learn which field paths that
// patch contributes, for the view's per-value origin -- without
// re-implementing this kind switch.
func DocumentFromPatch(d ichorv1alpha1.TalosConfigPatchDocument) (config.Document, error) {
	switch d.Kind {
	case "SysctlConfig":
		return wrapDocument("sysctlConfig")(d.SysctlConfig.ToMachinery())
	case "KernelModuleConfig":
		return wrapDocument("kernelModuleConfig")(d.KernelModuleConfig.ToMachinery())
	case "Config":
		return wrapDocument("config")(d.Config.ToMachinery())
	case "VolumeConfig":
		return wrapDocument("volumeConfig")(d.VolumeConfig.ToMachinery())
	case "UserVolumeConfig":
		return wrapDocument("userVolumeConfig")(d.UserVolumeConfig.ToMachinery())
	case "RawVolumeConfig":
		return wrapDocument("rawVolumeConfig")(d.RawVolumeConfig.ToMachinery())
	default:
		return nil, fmt.Errorf("unknown document kind %q", d.Kind)
	}
}

// wrapDocument returns a function that turns a ToMachinery result into DocumentFromPatch's
// return, naming the kind in an error and never returning a typed nil.
func wrapDocument(kind string) func(config.Document, error) (config.Document, error) {
	return func(doc config.Document, err error) (config.Document, error) {
		if err != nil {
			return nil, fmt.Errorf("%s: %w", kind, err)
		}

		return doc, nil
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
