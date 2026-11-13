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
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mruntime "github.com/siderolabs/talos/pkg/machinery/config/types/runtime"
	"golang.org/x/mod/semver"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// This file holds the Talos-side facts `ichor adopt` reads from a running
// cluster: the Talos version gate, the
// facts the machine configuration states about the CNI, kube-proxy,
// KubePrism, inline and external manifests and the install target, and the
// cluster membership. Everything here is read-only; the Kubernetes-side facts
// (DaemonSets, ConfigMaps, Helm records) belong to a different unit.

// adoptMinTalos is the oldest Talos minor `ichor adopt` accepts.
const adoptMinTalos = "v1.12"

// CNIKind says what a machine configuration selects as the cluster's CNI. It is what the
// configuration states, not what runs: the Kubernetes side decides that.
type CNIKind string

const (
	// CNIFlannel is Talos's own flannel: a KubeFlannelCNIConfig document, or
	// cluster.network.cni.name flannel, which is also the legacy default when the field is unset.
	CNIFlannel CNIKind = "flannel"
	// CNINone is cluster.network.cni.name: none, or a multi-document configuration with no
	// KubeFlannelCNIConfig and no legacy cni block: Talos installs nothing.
	CNINone CNIKind = "none"
	// CNICustom is cluster.network.cni.name: custom. Talos applies the manifests at URLs.
	CNICustom CNIKind = "custom"
	// CNIOther is any other cni.name the configuration carries.
	CNIOther CNIKind = "other"
)

// CNIFacts is the CNI selection in a machine configuration.
type CNIFacts struct {
	Kind CNIKind
	// Name is the raw cluster.network.cni.name (empty for the document form).
	Name string
	// URLs are the manifest URLs of cni.name: custom. A non-empty list is an unrepresentable
	// config path.
	URLs []string
}

// KubePrismFacts is the KubePrism setting (machine.features.kubePrism or the KubePrismConfig document).
type KubePrismFacts struct {
	Enabled bool
	Port    int
}

// InlineManifestFact is one inline manifest of the machine configuration, whichever form it is in
// (cluster.inlineManifests or a KubeInlineManifestConfig document). Contents is carried so a later
// unit can find Cilium by content rather than by name; it is never printed.
type InlineManifestFact struct {
	Name     string
	Contents string
}

// InstallFacts is the install target of a machine configuration.
type InstallFacts struct {
	Image string
	// Disk is machine.install.disk (the legacy form); empty if a selector is used instead.
	Disk string
	// DiskSelector is the CEL expression of the UnattendedInstallConfig document, or the legacy
	// installDiskSelector, when one is set.
	DiskSelector string
	// Wipe is the install-time wipe flag (UnattendedInstallConfig provisioning.wipe, machine.install.wipe).
	Wipe bool
}

// RegistryMirrorFact is one registry's mirror setting, whichever form (RegistryMirrorConfig
// document or machine.registries.mirrors) the configuration states it in.
type RegistryMirrorFact struct {
	Host         string
	Endpoints    []string
	SkipFallback bool
	// OverridePath is per endpoint in Talos; Ichor records one flag for all of a host's endpoints,
	// so a host with mixed values cannot be mapped (OverridePathMixed).
	OverridePath      bool
	OverridePathMixed bool
}

// TalosFacts is everything adopt reads from one node's machine configuration.
type TalosFacts struct {
	// Role is "controlplane" or "worker" as the configuration declares it.
	Role string

	CNI CNIFacts
	// KubeProxyEnabled is true when Talos runs kube-proxy (KubeProxyConfig.enabled, cluster.proxy.disabled).
	KubeProxyEnabled bool
	KubePrism        KubePrismFacts
	InlineManifests  []InlineManifestFact
	Install          InstallFacts
	// Hostname is the static hostname (HostnameConfig.hostname, machine.network.hostname); "" when none.
	Hostname string

	// CertSANs is machine.certSANs; APIServerCertSANs is the kube-apiserver's
	// (KubeAPIServerConfig.certExtraSANs, cluster.apiServer.certSANs), empty on a worker.
	CertSANs, APIServerCertSANs []string
	// NodeLabels are the node labels the configuration sets (KubeNodeConfig.labels, machine.nodeLabels).
	NodeLabels map[string]string
	// Registries are the mirror settings, sorted by host. Auth and TLS are not read: they have no
	// spec representation and stay in the diff.
	Registries []RegistryMirrorFact

	// What a TalosCluster needs when adopt has to create one (1.8 phase 2).
	PodSubnets, ServiceSubnets []string
	// EndpointHost is the host of cluster.controlPlane.endpoint.
	EndpointHost string
	// KubernetesVersion is the kubelet image's tag ("" if unreadable).
	KubernetesVersion string
	// ScheduleOnControlPlanes is cluster.allowSchedulingOnControlPlanes; nil when the form does not say.
	ScheduleOnControlPlanes *bool
}

// ReadTalosFacts decodes a node's machine configuration (the bytes talosConfigReader.CurrentConfig
// returns) into TalosFacts. It handles the legacy single-document form and the v1.13+
// multi-document form alike, through machinery's own container.
func ReadTalosFacts(cfgBytes []byte) (*TalosFacts, error) {
	provider, err := configloader.NewFromBytes(cfgBytes)
	if err != nil {
		return nil, fmt.Errorf("decoding the machine configuration: %w", err)
	}

	f := &TalosFacts{KubeProxyEnabled: true}

	if m := provider.Machine(); m != nil {
		f.Role = m.Type().String()

		if inst := m.Install(); inst != nil {
			f.Install.Image = inst.Image()
			f.Install.Disk = inst.Disk()
			f.Install.Wipe = inst.Zero()

			if expr, exprErr := inst.DiskMatchExpression(); exprErr == nil && expr != nil {
				f.Install.DiskSelector = expr.String()
			}
		}
	}

	// The v1.14 form is a document that Machine().Install() does not expose.
	for _, d := range provider.Documents() {
		if u, ok := d.(*mruntime.UnattendedInstallConfigV1Alpha1); ok {
			f.Install.Image = u.Installer.Image
			f.Install.DiskSelector = u.ProvisioningSpec.DiskSelector.Match.String()
			f.Install.Wipe = u.VolumeWipe()
		}
	}

	if m := provider.Machine(); m != nil && m.Security() != nil {
		f.CertSANs = m.Security().CertSANs()
	}

	if a := provider.K8sAPIServerConfig(); a != nil {
		f.APIServerCertSANs = a.CertSANs()
	}

	if n := provider.K8sNodeConfig(); n != nil {
		f.NodeLabels = n.Labels()
	}

	if h := provider.NetworkHostnameConfig(); h != nil {
		f.Hostname = h.Hostname()
	}

	f.Registries = registryFacts(provider)

	if p := provider.K8sProxyConfig(); p != nil {
		f.KubeProxyEnabled = p.Enabled()
	}

	if kp := provider.K8sKubePrismConfig(); kp != nil {
		f.KubePrism = KubePrismFacts{Enabled: true, Port: kp.Port()}
	}

	for _, im := range provider.K8sInlineManifestConfigs() {
		f.InlineManifests = append(f.InlineManifests, InlineManifestFact{Name: im.Name(), Contents: im.Contents()})
	}

	if n := provider.K8sNetworkConfig(); n != nil {
		for _, p := range n.PodCIDRs() {
			f.PodSubnets = append(f.PodSubnets, p.String())
		}

		for _, p := range n.ServiceCIDRs() {
			f.ServiceSubnets = append(f.ServiceSubnets, p.String())
		}
	}

	if c := provider.K8sClusterConfig(); c != nil && c.ClusterEndpoint() != nil {
		f.EndpointHost = c.ClusterEndpoint().Hostname()
	}

	if k := provider.K8sKubeletConfig(); k != nil {
		f.KubernetesVersion = imageTag(k.Image())
	}

	if raw := provider.RawV1Alpha1(); raw != nil && raw.ClusterConfig != nil { //nolint:staticcheck // the legacy block is where a pre-1.14 configuration states it
		f.ScheduleOnControlPlanes = raw.ClusterConfig.AllowSchedulingOnControlPlanes //nolint:staticcheck // as above
	}

	// The multi-document form states it as the control-plane taint on the node.
	if n := provider.K8sNodeConfig(); f.ScheduleOnControlPlanes == nil && n != nil {
		_, tainted := n.Taints()["node-role.kubernetes.io/control-plane"]
		schedulable := !tainted
		f.ScheduleOnControlPlanes = &schedulable
	}

	f.CNI = cniFacts(provider.K8sFlannelCNIConfig() != nil, provider)

	return f, nil
}

// registryFacts reads the mirror settings of either form, sorted by host.
func registryFacts(provider coreconfig.Provider) []RegistryMirrorFact {
	mirrors := provider.RegistryMirrorConfigs()
	out := make([]RegistryMirrorFact, 0, len(mirrors))

	for host, m := range mirrors {
		f := RegistryMirrorFact{Host: host, SkipFallback: m.SkipFallback()}

		for i, e := range m.Endpoints() {
			f.Endpoints = append(f.Endpoints, e.Endpoint())

			switch {
			case i == 0:
				f.OverridePath = e.OverridePath()
			case f.OverridePath != e.OverridePath():
				f.OverridePathMixed = true
			}
		}

		out = append(out, f)
	}

	slices.SortFunc(out, func(a, b RegistryMirrorFact) int { return strings.Compare(a.Host, b.Host) })

	return out
}

// cniFacts derives the CNI selection. flannelDoc is true when machinery reports a flannel
// configuration (the KubeFlannelCNIConfig document, or the legacy name flannel).
func cniFacts(flannelDoc bool, provider coreconfig.Provider) CNIFacts {
	if flannelDoc {
		return CNIFacts{Kind: CNIFlannel}
	}

	if raw := provider.RawV1Alpha1(); raw != nil && raw.ClusterConfig != nil && raw.ClusterConfig.ClusterNetwork != nil { //nolint:staticcheck // the legacy block is how a pre-1.14 configuration states its CNI
		cni := raw.ClusterConfig.CNI()
		out := CNIFacts{Name: cni.Name(), URLs: cni.URLs()}

		switch out.Name {
		case "none":
			out.Kind = CNINone
		case "custom":
			out.Kind = CNICustom
		case string(detectedFlannel), "":
			out.Kind = CNIFlannel
		default:
			out.Kind = CNIOther
		}

		return out
	}

	// No legacy network block. With a v1.13+ multi-document configuration, the absence of a
	// KubeFlannelCNIConfig document means Talos installs no CNI. A configuration that states
	// nothing about the network at all is Talos's default, flannel.
	for _, d := range provider.Documents() {
		if d.Kind() == k8s.KubeNetworkConfig {
			return CNIFacts{Kind: CNINone}
		}
	}

	return CNIFacts{Kind: CNIFlannel}
}

// ClusterMember is one node in the cluster's discovery membership.
type ClusterMember struct {
	Hostname  string
	Addresses []string
	// Role is "controlplane" or "worker".
	Role string
}

// talosVersionReader is implemented by a node reader that can report the node's Talos version.
// It is a separate interface so the read-only talosConfigReader stays the single required method.
type talosVersionReader interface {
	// TalosVersion returns the node's Talos version tag, for example "v1.12.4".
	TalosVersion(ctx context.Context) (string, error)
}

// TalosMembersReader is implemented by a node reader that can list the discovery membership of
// the cluster the node belongs to.
type TalosMembersReader interface {
	ClusterMembers(ctx context.Context) ([]ClusterMember, error)
}

// ErrTalosTooOld is wrapped by the version gate's error.
var ErrTalosTooOld = errors.New("talos is older than the oldest version Ichor adopts")

// nodeVersion is one node and the Talos version it runs.
type nodeVersion struct {
	Node    string
	Version string
}

// checkTalosVersions applies the v1.12 gate to the versions read from every node
// adopt will adopt. It returns nil if all are at or above v1.12 (pre-releases of v1.12 count as
// v1.12). Otherwise the error wraps ErrTalosTooOld, names every node below the floor, and says to
// upgrade Talos first. A version string that is not semver is an error too, never a pass.
func checkTalosVersions(nodes []nodeVersion) error {
	var older []nodeVersion

	for _, n := range nodes {
		mm := semver.MajorMinor(n.Version)
		if mm == "" {
			return fmt.Errorf("adopt: node %q reported the unreadable Talos version %q", n.Node, n.Version)
		}

		if semver.Compare(mm, adoptMinTalos) < 0 {
			older = append(older, n)
		}
	}

	if len(older) == 0 {
		return nil
	}

	slices.SortFunc(older, func(a, b nodeVersion) int { return strings.Compare(a.Node, b.Node) })

	const advice = "Upgrade Talos first, with talosctl or your usual tool, and run adopt again."

	if len(nodes) == 1 {
		n := older[0]

		return fmt.Errorf("%w: Talos %s is older than the oldest version Ichor adopts (%s). %s (node %q)",
			ErrTalosTooOld, strings.TrimPrefix(n.Version, "v"), adoptMinTalos, advice, n.Node)
	}

	parts := make([]string, 0, len(older))
	for _, n := range older {
		parts = append(parts, fmt.Sprintf("%s runs Talos %s", n.Node, strings.TrimPrefix(n.Version, "v")))
	}

	return fmt.Errorf("%w: %d of %d nodes are older than the oldest version Ichor adopts (%s): %s. %s",
		ErrTalosTooOld, len(older), len(nodes), adoptMinTalos, strings.Join(parts, "; "), advice)
}

// contractCandidates lists the version contracts to probe for a cluster whose nodes run running
// (any semver tag), highest first, never below v1.12 (1.7 and 1.9). A running version
// below the floor yields none.
func contractCandidates(running string) []string {
	mm := semver.MajorMinor(running)
	if mm == "" || semver.Compare(mm, adoptMinTalos) < 0 {
		return nil
	}

	var out []string

	for c := mm; semver.Compare(c, adoptMinTalos) >= 0; c = previousMinor(c) {
		out = append(out, c)
	}

	return out
}

// previousMinor returns the minor before mm ("v1.13" -> "v1.12"), or "" at minor 0.
func previousMinor(mm string) string {
	var major, minor int
	if _, err := fmt.Sscanf(mm, "v%d.%d", &major, &minor); err != nil || minor == 0 {
		return ""
	}

	return fmt.Sprintf("v%d.%d", major, minor-1)
}

// gateTalosVersions reads the Talos version of every machine adopt will adopt and applies the
// v1.12 gate before anything else happens. A node whose reader cannot report a
// version (a test double that does not implement talosVersionReader) is not gated.
func gateTalosVersions(
	ctx context.Context,
	machines []*ichorv1alpha1.TalosMachine,
	dial func(ctx context.Context, endpoint string) (talosConfigReader, error),
) error {
	var nodes []nodeVersion

	for _, m := range machines {
		talos, err := dial(ctx, m.DialEndpoint())
		if err != nil {
			return fmt.Errorf("adopt: dialing node %q at %q: %w", m.Name, m.DialEndpoint(), err)
		}

		vr, ok := talos.(talosVersionReader)
		if !ok {
			continue
		}

		v, err := vr.TalosVersion(ctx)
		if err != nil {
			return fmt.Errorf("adopt: reading node %q's Talos version: %w", m.Name, err)
		}

		nodes = append(nodes, nodeVersion{Node: m.Name, Version: v})
	}

	return checkTalosVersions(nodes)
}

// ReadClusterMembers lists the discovery membership through talos, or returns nil, false if the
// reader cannot (1.4: the node list and roles come from membership, not from typing
// every address).
func ReadClusterMembers(ctx context.Context, talos talosConfigReader) ([]ClusterMember, bool, error) {
	mr, ok := talos.(TalosMembersReader)
	if !ok {
		return nil, false, nil
	}

	members, err := mr.ClusterMembers(ctx)

	return members, true, err
}
