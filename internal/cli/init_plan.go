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
	"io"
	"net/netip"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniPatchLayer is the TalosConfigPatch layer every patch init builds for
// the CNI or the inline-manifests bootstrap carries: "cluster", the
// broadest layer, since neither applies to just one machine.
const cniPatchLayer = "cluster"

// cniKey names the CNI consistently wherever init refers to it by a
// short key rather than a full flag name: the "--cni" flag itself, the
// cluster.inlineManifests entry's name.
const cniKey = "cni"

// defaultPreset is the preset architecture.md's "Nodes" section marks
// "recommended" and the one `--preset`'s flag default uses.
const defaultPreset = "standard"

// ichorControllerNamespace is the fixed namespace Ichor's own install
// manifest (internal/installmanifest) always deploys the controller
// into -- config/manager's kustomize base names it literally, matching
// the RBAC markers' own "ichor-system" literal (ADR 0011). init's
// --namespace flag defaults to this and warns if overridden, since a
// Secret or CR created anywhere else is one the deployed controller
// cannot see (it resolves the bundle Secret in its own namespace, not
// the TalosCluster's).
const ichorControllerNamespace = "ichor-system"

// schedulableTrue and schedulableFalse are TalosClusterControlPlane.
// Schedulable's two valid string values (ADR 0008: no default).
const (
	schedulableTrue  = "true"
	schedulableFalse = "false"
)

// topology is one entry of presetTopology: the control-plane count and
// schedulable value a preset implies.
type topology struct {
	ControlPlaneCount int32
	Schedulable       string // schedulableTrue or schedulableFalse
}

// presetTopology maps each of the four topology presets
// architecture.md's "Nodes" section names to the control-plane count and
// schedulable value it implies:
//
//	single      (1+0, schedulable)
//	compact-ha  (3+0, schedulable)
//	standard    (3+n, tainted) -- recommended
//	large       (5+n, tainted)
//
// "+0"/"+n" is workers: single and compact-ha never have any (the control
// plane is the whole cluster, so it has to accept workloads or nothing
// could ever run); standard and large are sized for separate worker
// nodes added later through the normal approval flow, so their control
// planes are tainted. This pairing is also what keeps this table from
// ever producing the one combination the design explicitly rejects --
// "making control planes unschedulable with zero workers" -- every
// schedulable=false entry here is one whose preset promises workers;
// TestPresetTopology_NeverRejectsSchedulableWithNoWorkers checks the
// table itself, not just today's four rows, stays that way.
var presetTopology = map[string]topology{
	"single":     {ControlPlaneCount: 1, Schedulable: schedulableTrue},
	"compact-ha": {ControlPlaneCount: 3, Schedulable: schedulableTrue},
	"standard":   {ControlPlaneCount: 3, Schedulable: schedulableFalse},
	"large":      {ControlPlaneCount: 5, Schedulable: schedulableFalse},
}

// presetHasWorkers reports whether preset's topology is sized for
// separate worker nodes ("+n" above) -- the only presets allowed to pair
// with schedulable=false, since a control plane that is both
// unschedulable and the only kind of node in the cluster could never run
// a workload.
func presetHasWorkers(preset string) bool {
	return preset == "standard" || preset == "large"
}

// initPlanInput bundles the raw flag values initCmd's RunE collects, so
// buildInitPlan is driven by plain values a test can construct directly,
// with no cobra, filesystem or network involved.
type initPlanInput struct {
	Cluster   string
	Namespace string

	Preset    string
	CNIPreset string // ichorv1alpha1.TalosClusterCNI.Preset: only "cilium" (ADR 0099)
	CNITunnel string // --cni-tunnel: "vxlan" or "geneve"; only valid with cilium; "" keeps Cilium's default

	EndpointType    string
	EndpointAddress string

	TalosVersion      string
	KubernetesVersion string
	VersionContract   string

	// InstallDisk is the first node's install disk (machine.install.disk,
	// the "system" role -- TalosMachineSpec still carries no field for
	// the other three roles; see buildInitPlan's doc comment). Required:
	// without it the node cannot install itself non-interactively.
	InstallDisk string

	// PodSubnets and ServiceSubnets set TalosCluster.spec.network (ADR 0077); empty keeps
	// Talos's defaults.
	PodSubnets     []string
	ServiceSubnets []string

	// RegistryMirrors are "HOST=URL" pairs for spec.registries: anonymous mirrors only, since
	// a new cluster has no Secret to read credentials from yet.
	RegistryMirrors []string

	// DiscoverAddresses and DiscoverCIDRs set TalosCluster.spec.discovery (ADR 0011), so nodes
	// booted into maintenance mode after init are found; empty leaves discovery off.
	DiscoverAddresses []string
	DiscoverCIDRs     []string
}

// CRD limits of spec.discovery, checked here the way its CEL rules do.
const (
	maxDiscoverAddresses = 64
	maxDiscoverCIDRs     = 4
	minDiscoverPrefix    = 24
)

func validateDiscovery(addresses, cidrs []string) error {
	if len(addresses) > maxDiscoverAddresses {
		return fmt.Errorf("init: --discover-address takes at most %d addresses", maxDiscoverAddresses)
	}

	if len(cidrs) > maxDiscoverCIDRs {
		return fmt.Errorf("init: --discover-cidr takes at most %d CIDRs", maxDiscoverCIDRs)
	}

	for _, a := range addresses {
		if ip, err := netip.ParseAddr(a); err != nil || !ip.Is4() {
			return fmt.Errorf("init: --discover-address %q is not an IPv4 address", a)
		}
	}

	for _, c := range cidrs {
		if p, err := netip.ParsePrefix(c); err != nil || !p.Addr().Is4() || p.Bits() < minDiscoverPrefix {
			return fmt.Errorf("init: --discover-cidr %q must be an IPv4 CIDR of /%d or smaller", c, minDiscoverPrefix)
		}
	}

	return nil
}

// maxSubnetsPerRange is the CRD's limit: one CIDR per IP family.
const maxSubnetsPerRange = 2

func validateCIDRs(flag string, cidrs []string) error {
	if len(cidrs) > maxSubnetsPerRange {
		return fmt.Errorf("init: %s takes at most %d CIDRs (one per IP family)", flag, maxSubnetsPerRange)
	}

	for _, c := range cidrs {
		if _, err := netip.ParsePrefix(c); err != nil {
			return fmt.Errorf("init: %s %q is not a CIDR: %w", flag, c, err)
		}
	}

	return nil
}

func parseRegistryMirrors(specs []string) ([]ichorv1alpha1.TalosClusterRegistry, error) {
	var out []ichorv1alpha1.TalosClusterRegistry

	for _, s := range specs {
		host, endpoint, ok := strings.Cut(s, "=")
		if !ok || host == "" || endpoint == "" {
			return nil, fmt.Errorf("init: --registry-mirror %q must be HOST=URL", s)
		}

		out = append(out, ichorv1alpha1.TalosClusterRegistry{Host: host, Endpoints: []string{endpoint}})
	}

	return out, nil
}

// secretsRefName is the Secret name buildInitPlan assigns to
// TalosCluster.Spec.SecretsRef; it lives in the controller's own namespace
// once created (ADR 0011), not necessarily Namespace above, which is just
// where this TalosCluster itself is created.
func secretsRefName(cluster string) string {
	return cluster + "-secrets"
}

// buildInitPlan implements Bootstrap step 2, "settle the shape of the
// cluster": it validates in and returns the TalosCluster object init will
// render against and create, with controlPlane.count/schedulable derived
// from --preset (architecture.md's "Nodes": "the topology preset that
// carries the intent is a flag on ichor init, not a field (ADR 0008);
// init sets it from the preset"). It writes a warning (never an error) to
// warn when the endpoint type is not "vip" or the namespace is not
// ichorControllerNamespace, per architecture.md's "Design" ("a VIP is
// recommended... because changing the endpoint later means re-issuing
// certificates") and ADR 0019 (the fixed controller namespace) --
// recommended, not required, so this never rejects a valid-but-unusual
// choice on its own.
//
// Of step 2's named disk roles, only "system" (the install disk) is set
// here -- it is folded into the legacy Config patch alongside the
// cluster.inlineManifests entry, in buildInlineManifestsPatch, since
// machine.install and cluster.inlineManifests are both on the same
// document. The other three roles (ephemeral/userVolume/storage,
// VolumeConfigV1Alpha1/UserVolumeConfigV1Alpha1/RawVolumeConfigV1Alpha1)
// are now representable in the mirror type's allow-list, but init does
// not expose flags for them yet: a useful per-disk role selection UI is
// a larger, separate piece of work than fits alongside finishing this
// phase, and leaving it unbuilt is honest; inventing a narrow one-disk
// flag for each just to say something is wired would not actually cover
// what an operator needs. See ADR 0019.
func buildInitPlan(in initPlanInput, warn io.Writer) (*ichorv1alpha1.TalosCluster, error) {
	// --cluster becomes the TalosCluster's own metadata.name, so it has to
	// be a valid Kubernetes object name regardless; checking that here,
	// before runInit's step 1 writes the secrets bundle to
	// ~/.ichor/clusters/<cluster>/ (bundlefile.go), means a bad value is
	// refused before it reaches a filesystem path built from it, not
	// after -- the same constraint, just applied at the point that can
	// still refuse cleanly instead of writing to the wrong place first.
	if errs := validation.IsDNS1123Subdomain(in.Cluster); len(errs) > 0 {
		return nil, fmt.Errorf("init: --cluster %q is invalid: %s (it becomes the TalosCluster's own Kubernetes object name)",
			in.Cluster, strings.Join(errs, "; "))
	}

	top, ok := presetTopology[in.Preset]
	if !ok {
		return nil, fmt.Errorf("init: --preset %q is invalid: must be one of single, compact-ha, standard (recommended) or large", in.Preset)
	}

	if top.Schedulable == schedulableFalse && !presetHasWorkers(in.Preset) {
		// Unreachable through presetTopology's own four rows today (see
		// its doc comment); kept as the actual enforcement point in case
		// that table is ever edited incorrectly, so the rejection
		// architecture.md requires has somewhere real to live.
		return nil, fmt.Errorf(
			"init: preset %q would make control planes unschedulable with no workers ever added; rejected", in.Preset)
	}

	if in.CNIPreset != cniCilium {
		return nil, fmt.Errorf("init: --cni %q is invalid: cilium is the only CNI (ADR 0099)", in.CNIPreset)
	}

	switch in.CNITunnel {
	case "":
	case ciliumTunnelVxlan, ciliumTunnelGeneve:
	default:
		return nil, fmt.Errorf("init: --cni-tunnel %q is invalid: must be %q or %q", in.CNITunnel, ciliumTunnelVxlan, ciliumTunnelGeneve)
	}

	switch in.EndpointType {
	case "vip", "loadBalancer", "dns", "node":
	default:
		return nil, fmt.Errorf("init: --endpoint-type %q is invalid: must be one of vip, loadBalancer, dns, node", in.EndpointType)
	}

	if in.EndpointType != "vip" {
		_, _ = fmt.Fprintf(warn,
			"warning: endpoint type %q: a VIP is recommended — changing it later re-issues every certificate\n",
			in.EndpointType)
	}

	if in.EndpointAddress == "" {
		return nil, fmt.Errorf("init: --endpoint-address is required")
	}

	if in.Namespace != ichorControllerNamespace {
		_, _ = fmt.Fprintf(warn,
			"warning: --namespace %q is not %q: Ichor's controller always runs in %q, "+
				"so a Secret or CR created elsewhere is not visible to it\n",
			in.Namespace, ichorControllerNamespace, ichorControllerNamespace)
	}

	if in.InstallDisk == "" {
		return nil, fmt.Errorf("init: --install-disk is required")
	}

	if err := validateCIDRs("--pod-subnets", in.PodSubnets); err != nil {
		return nil, err
	}

	if err := validateCIDRs("--service-subnets", in.ServiceSubnets); err != nil {
		return nil, err
	}

	registries, err := parseRegistryMirrors(in.RegistryMirrors)
	if err != nil {
		return nil, err
	}

	if err := validateDiscovery(in.DiscoverAddresses, in.DiscoverCIDRs); err != nil {
		return nil, err
	}

	var discovery *ichorv1alpha1.TalosClusterDiscovery
	if len(in.DiscoverAddresses) > 0 || len(in.DiscoverCIDRs) > 0 {
		discovery = &ichorv1alpha1.TalosClusterDiscovery{Addresses: in.DiscoverAddresses, CIDRs: in.DiscoverCIDRs}
	}

	var network *ichorv1alpha1.TalosClusterNetwork
	if len(in.PodSubnets) > 0 || len(in.ServiceSubnets) > 0 {
		network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: in.PodSubnets, ServiceSubnets: in.ServiceSubnets}
	}

	return &ichorv1alpha1.TalosCluster{
		Name: in.Cluster, Namespace: in.Namespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Network:           network,
			Registries:        registries,
			Discovery:         discovery,
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: in.EndpointType, Address: in.EndpointAddress},
			TalosVersion:      in.TalosVersion,
			KubernetesVersion: in.KubernetesVersion,
			VersionContract:   in.VersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretsRefName(in.Cluster)},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: top.ControlPlaneCount, Schedulable: top.Schedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: in.CNIPreset, KubeProxy: "enabled", Tunnel: in.CNITunnel},
		},
	}, nil
}
