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
	"net"
	"net/netip"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// cniPatchLayer is the TalosConfigPatch layer every patch init builds for
// the CNI or the inline-manifests bootstrap carries: "cluster", the
// broadest layer, since neither applies to just one machine.
const cniPatchLayer = "cluster"

// macPattern is the form TalosMachineNetwork.mac takes.
var macPattern = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)

// defaultPreset is the preset architecture.md's "Nodes" section marks
// "recommended" and the one `--preset`'s flag default uses.
const defaultPreset = "standard"

// ichorControllerNamespace is the fixed namespace Ichor's own install
// manifest (internal/installmanifest) always deploys the controller
// into -- config/manager's kustomize base names it literally, matching
// the RBAC markers' own "ichor-system" literal. init's
// --namespace flag defaults to this and warns if overridden, since a
// Secret or CR created anywhere else is one the deployed controller
// cannot see (it resolves the bundle Secret in its own namespace, not
// the TalosCluster's).
const ichorControllerNamespace = "ichor-system"

// schedulableTrue and schedulableFalse are TalosClusterControlPlane.
// Schedulable's two valid string values (no default).
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
	CNIPreset string // ichorv1alpha1.TalosClusterCNI.Preset: only "cilium"
	CNITunnel string // --cni-tunnel: "vxlan" or "geneve"; only valid with cilium; "" keeps Cilium's default
	// KubeProxy (--kube-proxy) is "enabled" (Talos's own kube-proxy, the default) or "replaced" (Cilium replaces it).
	KubeProxy string

	EndpointType    string
	EndpointAddress string

	// NIC is the first node's primary link by permanent MAC (--nic); it only means something for a vip,
	// whose Layer2VIPConfig is announced on that link.
	NIC string

	// Addresses, Gateway, DNS and VLAN address that link (--address, --gateway, --dns, --vlan), as on
	// `approve node`; with none of them the node uses DHCP on it.
	Addresses []string
	Gateway   string
	DNS       []string
	VLAN      int32

	TalosVersion      string
	KubernetesVersion string
	VersionContract   string

	// InstallDisk is the first node's install disk (machine.install.disk,
	// the "system" role -- TalosMachineSpec still carries no field for
	// the other three roles; see buildInitPlan's doc comment). Required:
	// without it the node cannot install itself non-interactively.
	InstallDisk string

	// PodSubnets and ServiceSubnets set TalosCluster.spec.network; empty keeps
	// Talos's defaults.
	PodSubnets     []string
	ServiceSubnets []string

	// RegistryMirrors are "HOST=URL" pairs for spec.registries: anonymous mirrors only, since
	// a new cluster has no Secret to read credentials from yet.
	RegistryMirrors []string

	// DiscoverAddresses and DiscoverCIDRs set TalosCluster.spec.discovery, so nodes
	// booted into maintenance mode after init are found; empty leaves discovery off.
	DiscoverAddresses []string
	DiscoverCIDRs     []string

	// CertSANs set TalosCluster.spec.certSANs: extra names and addresses that both the Talos API
	// and the kube-apiserver certificates carry. Changing the list after init reboots the control
	// planes, so it is set here; empty adds none.
	CertSANs []string

	// GatewayAPI (--gateway-api) turns on Cilium's Gateway API and puts its CRDs in the bootstrap. L2Announcements
	// (--l2-announcements) turns on L2 announcements, announced from L2Pool (--l2-pool, an IPv4 CIDR). Init only.
	GatewayAPI      bool
	L2Announcements bool
	L2Pool          string
}

// CRD limits of spec.certSANs (api/v1alpha1), checked here before anything is written to a node.
const (
	maxCertSANs      = 64
	maxCertSANLength = 253
)

func validateCertSANs(sans []string) error {
	if len(sans) > maxCertSANs {
		return fmt.Errorf("init: --cert-san takes at most %d names", maxCertSANs)
	}

	for _, s := range sans {
		if s == "" || len(s) > maxCertSANLength {
			return fmt.Errorf("init: --cert-san %q must be 1 to %d characters", s, maxCertSANLength)
		}
	}

	return nil
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
// once created, not necessarily Namespace above, which is just
// where this TalosCluster itself is created.
func secretsRefName(cluster string) string {
	return cluster + "-secrets"
}

// buildInitPlan implements Bootstrap step 2, "settle the shape of the
// cluster": it validates in and returns the TalosCluster object init will
// render against and create, with controlPlane.count/schedulable derived
// from --preset (architecture.md's "Nodes": "the topology preset that
// carries the intent is a flag on ichor init, not a field;
// init sets it from the preset"). It writes a warning (never an error) to
// warn when the endpoint type is not "vip" or the namespace is not
// ichorControllerNamespace, per architecture.md's "Design" ("a VIP is
// recommended... because changing the endpoint later means re-issuing
// certificates") (the fixed controller namespace) --
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
// what an operator needs.
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
		return nil, fmt.Errorf("init: --cni %q is invalid: cilium is the only CNI", in.CNIPreset)
	}

	switch in.CNITunnel {
	case "":
	case ciliumTunnelVxlan, ciliumTunnelGeneve:
	default:
		return nil, fmt.Errorf("init: --cni-tunnel %q is invalid: must be %q or %q", in.CNITunnel, ciliumTunnelVxlan, ciliumTunnelGeneve)
	}

	if !validEndpointType(in.EndpointType) {
		return nil, fmt.Errorf("init: --endpoint-type %q is invalid: must be one of %s", in.EndpointType, endpointTypeNames(", ", ", "))
	}

	if in.EndpointType != endpointVIP {
		_, _ = fmt.Fprintf(warn,
			"warning: endpoint type %q: a VIP is recommended — changing it later re-issues every certificate\n",
			in.EndpointType)
	}

	if in.EndpointAddress == "" {
		return nil, fmt.Errorf("init: --endpoint-address is required")
	}

	// The webhook refuses the same pair; here it is refused before anything is created.
	if err := (ichorv1alpha1.TalosClusterEndpoint{Type: in.EndpointType, Address: in.EndpointAddress}).CheckAddress(); err != nil {
		return nil, fmt.Errorf("init: %w", err)
	}

	if err := checkVIPInput(in); err != nil {
		return nil, err
	}

	if in.Namespace != ichorControllerNamespace {
		_, _ = fmt.Fprintf(warn,
			"warning: --namespace %q is not %q: Ichor's controller always runs in %q, "+
				"so a Secret or CR created elsewhere is not visible to it\n",
			in.Namespace, ichorControllerNamespace, ichorControllerNamespace)
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

	if err := validateCertSANs(in.CertSANs); err != nil {
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

	cilium, err := ciliumUserValues(in)
	if err != nil {
		return nil, err
	}

	return &ichorv1alpha1.TalosCluster{
		Name: in.Cluster, Namespace: in.Namespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Network:           network,
			Registries:        registries,
			Discovery:         discovery,
			CertSANs:          in.CertSANs,
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: in.EndpointType, Address: in.EndpointAddress},
			TalosVersion:      in.TalosVersion,
			KubernetesVersion: in.KubernetesVersion,
			VersionContract:   in.VersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretsRefName(in.Cluster)},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: top.ControlPlaneCount, Schedulable: top.Schedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: in.CNIPreset, KubeProxy: string(in.kubeProxyMode()), Tunnel: in.CNITunnel, Cilium: cilium},
		},
	}, nil
}

// checkVIPInput refuses a --nic or static-address flag that no VIP would use, and a --nic that is neither a link name
// nor a MAC written with colons.
func checkVIPInput(in initPlanInput) error {
	if in.EndpointType != endpointVIP {
		if in.NIC != "" {
			return fmt.Errorf("init: --nic is only used with --endpoint-type vip, not %q", in.EndpointType)
		}

		if len(in.Addresses) > 0 || in.Gateway != "" || len(in.DNS) > 0 || in.VLAN != 0 {
			return fmt.Errorf("init: --address, --gateway, --dns and --vlan are only used with --endpoint-type vip, not %q", in.EndpointType)
		}
	}

	if in.NIC == "" {
		return nil
	}

	// Anything that is not a MAC is a link name, looked up once the node's links are read.
	if _, err := net.ParseMAC(in.NIC); err == nil && !macPattern.MatchString(in.NIC) {
		return fmt.Errorf("init: --nic %q is not a MAC address like aa:bb:cc:dd:ee:ff", in.NIC)
	}

	return nil
}

// planInitCluster is buildInitPlan plus the check that needs the node's address.
func planInitCluster(opts initOptions, warn io.Writer) (*ichorv1alpha1.TalosCluster, error) {
	cluster, err := buildInitPlan(opts.initPlanInput, warn)
	if err != nil {
		return nil, err
	}

	return cluster, checkVIPNotNode(cluster, opts.NodeEndpoint)
}

// checkVIPNotNode refuses a vip endpoint address equal to the first node's own address: the VIP would be a
// second address on the link the node is reached by, and the address that must move between control planes
// would be the one the node was given.
func checkVIPNotNode(cluster *ichorv1alpha1.TalosCluster, nodeEndpoint string) error {
	if cluster.Spec.Endpoint.Type != endpointVIP {
		return nil
	}

	host := nodeEndpoint
	if h, _, err := net.SplitHostPort(nodeEndpoint); err == nil {
		host = h
	}

	vip, err1 := netip.ParseAddr(cluster.Spec.Endpoint.Address)
	node, err2 := netip.ParseAddr(host)

	if err1 == nil && err2 == nil && vip.Unmap() == node.Unmap() {
		return fmt.Errorf("init: --endpoint-address %s is the first node's own address (--node-endpoint %s); "+
			"a vip is an extra address that moves between control planes, so it needs one nobody has yet", vip, nodeEndpoint)
	}

	return nil
}
