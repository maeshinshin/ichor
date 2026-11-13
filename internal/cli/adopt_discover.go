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
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// adoptNode is one machine adopt looks at: the (possibly not yet written) TalosMachine plus what
// was read from the node.
type adoptNode struct {
	M *ichorv1alpha1.TalosMachine
	// Skipped is why adopt did not read the node (not approved).
	Skipped string
	// added marks a machine --add-machine registers: its TalosMachine does not exist yet.
	added bool

	Current []byte
	Facts   *TalosFacts
	// InstallDisk is the facts' install disk as the write phase takes it ("" if the node has no
	// plain device path).
	InstallDisk string
}

// hostnameRE is the pattern of TalosMachine.spec.hostname.
var hostnameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

func (n *adoptNode) controlPlane() bool { return n.M.Spec.Role == roleControlPlane }

// discoverMachines builds the in-memory TalosMachines of a cluster Ichor has not seen yet, from
// the discovery membership of the seed node (1.4: the node list comes from membership,
// not from typing every address). Names are the hardware UUID, as the write phase will create
// them. Nothing is written.
func discoverMachines(ctx context.Context, deps adoptDeps, clusterName, namespace string) ([]*ichorv1alpha1.TalosMachine, []ClusterMember, error) {
	if deps.Seed == "" {
		return nil, nil, fmt.Errorf("adopt: TalosCluster %q does not exist yet; give --node, the address of one control plane, "+
			"so adopt can discover the cluster's nodes", clusterName)
	}

	if deps.ReadIdentity == nil {
		return nil, nil, errors.New("adopt: no way to read the nodes' hardware identity was given")
	}

	seed, err := deps.DialTalos(ctx, deps.Seed)
	if err != nil {
		return nil, nil, fmt.Errorf("adopt: dialing %q: %w", deps.Seed, err)
	}

	members, ok, err := ReadClusterMembers(ctx, seed)
	if err != nil {
		return nil, nil, fmt.Errorf("adopt: listing the cluster members: %w", err)
	}

	if !ok || len(members) == 0 {
		members = []ClusterMember{{Addresses: []string{deps.Seed}}}
	}

	machines := make([]*ichorv1alpha1.TalosMachine, 0, len(members))

	for _, mem := range members {
		if len(mem.Addresses) == 0 {
			return nil, nil, fmt.Errorf("adopt: cluster member %q reports no address", mem.Hostname)
		}

		addr := mem.Addresses[0]

		id, err := deps.ReadIdentity(ctx, addr)
		if err != nil {
			return nil, nil, fmt.Errorf("adopt: reading the hardware identity of %q: %w", addr, err)
		}

		if id.UUID == "" {
			return nil, nil, fmt.Errorf("adopt: the node at %q reported no SMBIOS UUID; adopt needs a strong hardware identity", addr)
		}

		machines = append(machines, &ichorv1alpha1.TalosMachine{
			Name: id.UUID, Namespace: namespace,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: id.UUID, Serial: id.Serial, MACAddresses: id.MACAddresses},
				Endpoint:      addr,
				Role:          mem.Role,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		})
	}

	return machines, members, nil
}

// checkMembership refuses an adoption whose discovered membership and the workload cluster's
// Nodes disagree: a node that registered but is not yet in Talos discovery would otherwise be
// silently left out. It runs before anything is written. A nil target skips the check.
// shortcut: an existing TalosCluster's machines are not cross-checked; their endpoints need not
// be Node addresses and unapproved machines are legitimate.
func checkMembership(ctx context.Context, target client.Reader, machines []*ichorv1alpha1.TalosMachine, members []ClusterMember) error {
	if target == nil {
		return nil
	}

	var nodes corev1.NodeList
	if err := target.List(ctx, &nodes); err != nil {
		return fmt.Errorf("adopt: listing the cluster's Nodes: %w", err)
	}

	// The seed-only fallback has no hostname; the machines carry the address it was found at.
	if len(members) == 0 {
		for _, m := range machines {
			members = append(members, ClusterMember{Addresses: []string{m.Spec.Endpoint}})
		}
	}

	var noMember, noNode []string

	for i := range nodes.Items {
		if !slices.ContainsFunc(members, func(m ClusterMember) bool { return memberIsNode(m, &nodes.Items[i]) }) {
			noMember = append(noMember, nodes.Items[i].Name)
		}
	}

	for _, m := range members {
		if !slices.ContainsFunc(nodes.Items, func(n corev1.Node) bool { return memberIsNode(m, &n) }) {
			noNode = append(noNode, cmp.Or(m.Hostname, strings.Join(m.Addresses, ",")))
		}
	}

	if len(noMember) == 0 && len(noNode) == 0 {
		return nil
	}

	return fmt.Errorf("adopt: Nodes without a discovery member: [%s]; discovery members without a Node: [%s]; "+
		"discovery membership is incomplete; wait until every node is listed (talosctl get members) and run adopt again; nothing was written",
		strings.Join(noMember, ", "), strings.Join(noNode, ", "))
}

// memberIsNode matches by hostname (the Node name, or its short form) or by any shared address.
func memberIsNode(m ClusterMember, n *corev1.Node) bool {
	if m.Hostname != "" {
		short, _, _ := strings.Cut(m.Hostname, ".")
		if strings.EqualFold(m.Hostname, n.Name) || strings.EqualFold(short, n.Name) {
			return true
		}
	}

	return slices.ContainsFunc(n.Status.Addresses, func(a corev1.NodeAddress) bool { return slices.Contains(m.Addresses, a.Address) })
}

// completeNode fills what only the node's configuration says: the role when membership gave none,
// and the install disk.
func completeNode(n *adoptNode, warn func(format string, args ...any)) error {
	if n.M.Spec.Role == "" {
		n.M.Spec.Role = n.Facts.Role
	}

	// spec.hostname is an RFC 1123 name; one the CRD would reject is left as drift, not a failed create.
	if h := n.Facts.Hostname; n.M.Spec.Hostname == "" && h != "" {
		if hostnameRE.MatchString(h) && len(h) <= 253 {
			n.M.Spec.Hostname = h
		} else {
			warn("node %q: its hostname %q is not an RFC 1123 name, so it is not taken into spec.hostname and stays as drift", n.M.Name, h)
		}
	}

	// The legacy form names a disk path or a selector, v1.14 a CEL selector; a selector that is
	// not one of the equalities spec.install.disk can hold is left as drift.
	diskArg := cmp.Or(n.Facts.Install.Disk, diskFromExpression(n.Facts.Install.DiskSelector))

	if n.M.Spec.Install != nil || diskArg == "" {
		return nil
	}

	disk, err := parseDiskSelector("--install-disk", diskArg)
	if err != nil {
		return fmt.Errorf("adopt: node %q: %w", n.M.Spec.Endpoint, err)
	}

	n.InstallDisk = diskArg
	n.M.Spec.Install = &ichorv1alpha1.TalosMachineInstall{Disk: *disk, Image: n.Facts.Install.Image, Wipe: n.Facts.Install.Wipe}

	return nil
}

// clusterFromNodes builds the TalosCluster adopt will create from what the nodes' configurations
// say. The CNI fields and the version contract are set afterwards, by the inspection and the
// probing.
func clusterFromNodes(name, namespace, talosVersion string, nodes []*adoptNode) (*ichorv1alpha1.TalosCluster, error) {
	var (
		cp      *adoptNode
		cpCount int32
	)

	for _, n := range nodes {
		switch {
		case n.Skipped != "":
		case n.controlPlane():
			cpCount++

			if cp == nil {
				cp = n
			}
		}
	}

	if cp == nil {
		return nil, errors.New("adopt: no control plane among the cluster's nodes")
	}

	f := cp.Facts

	// A name, and an address that is no node's own, is in front of the nodes, which is what loadBalancer takes.
	// It is never vip: that type means Ichor puts the address on the control planes (pkg/render), and adopt
	// must never claim an address it did not configure.
	endpointType := endpointLoadBalancer

	if addr, err := netip.ParseAddr(f.EndpointHost); err == nil &&
		slices.ContainsFunc(nodes, func(n *adoptNode) bool { return n.M.Spec.Endpoint == addr.String() }) {
		endpointType = endpointNode
	}

	if f.EndpointHost == "" {
		return nil, errors.New("adopt: the control plane's configuration names no cluster endpoint")
	}

	// Talos's default, when the configuration does not say, is not to schedule on control planes.
	schedulable := "false"
	if f.ScheduleOnControlPlanes != nil {
		schedulable = fmt.Sprint(*f.ScheduleOnControlPlanes)
	}

	c := &ichorv1alpha1.TalosCluster{
		Name: name, Namespace: namespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: endpointType, Address: f.EndpointHost},
			TalosVersion:      talosVersion,
			KubernetesVersion: kubernetesVersionOr(f.KubernetesVersion),
			SecretsRef:        corev1.LocalObjectReference{Name: secretsRefName(name)},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: cpCount, Schedulable: schedulable},
		},
	}

	if len(f.PodSubnets) > 0 || len(f.ServiceSubnets) > 0 {
		c.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: f.PodSubnets, ServiceSubnets: f.ServiceSubnets}
	}

	c.Spec.CertSANs = agreedCertSANs(nodes)
	c.Spec.Registries = agreedRegistries(nodes)
	c.Spec.ControlPlane.OmitDefaultNodeLabels = controlPlanesLackExcludeLabel(nodes)

	if cands := contractCandidates(talosVersion); len(cands) > 0 {
		c.Spec.VersionContract = cands[0]
	}

	return c, nil
}

// agreedCertSANs is spec.certSANs. The render feeds that one list into both machine.certSANs and the
// kube-apiserver SANs of every node, and a patch can add a SAN but not take one away, so it holds only
// the SANs every node has in both places. What a node has beyond it is captured into the node's own
// patch (ADR 0143), so no certificate changes.
func agreedCertSANs(nodes []*adoptNode) []string {
	var (
		sans  []string
		first = true
	)

	keep := func(list []string) {
		if first {
			sans, first = slices.Clone(list), false

			return
		}

		sans = slices.DeleteFunc(sans, func(s string) bool { return !slices.Contains(list, s) })
	}

	for _, n := range readNodes(nodes) {
		keep(n.Facts.CertSANs)

		if n.controlPlane() {
			keep(n.Facts.APIServerCertSANs)
		}
	}

	return sans
}

// controlPlanesLackExcludeLabel is spec.controlPlane.omitDefaultNodeLabels: true when no control
// plane carries Talos's default exclude-from-external-load-balancers label. Adding it would take
// the nodes out of Service type=LoadBalancer membership, so adopt keeps them as they are (ADR 0144).
func controlPlanesLackExcludeLabel(nodes []*adoptNode) bool {
	cps := slices.DeleteFunc(readNodes(nodes), func(n *adoptNode) bool { return !n.controlPlane() })

	return len(cps) > 0 && !slices.ContainsFunc(cps, func(n *adoptNode) bool {
		_, has := n.Facts.NodeLabels[constants.LabelExcludeFromExternalLB]

		return has
	})
}

// agreedRegistries is spec.registries: the mirrors when every node states the same ones and none
// mixes overridePath among one host's endpoints. Credentials and TLS are never read: they have no
// representation here and stay in the diff.
func agreedRegistries(nodes []*adoptNode) []ichorv1alpha1.TalosClusterRegistry {
	var want []RegistryMirrorFact

	for i, n := range readNodes(nodes) {
		if i == 0 {
			want = n.Facts.Registries
		}

		if !slices.EqualFunc(want, n.Facts.Registries, func(a, b RegistryMirrorFact) bool {
			return a.Host == b.Host && slices.Equal(a.Endpoints, b.Endpoints) && a.SkipFallback == b.SkipFallback &&
				a.OverridePath == b.OverridePath && a.OverridePathMixed == b.OverridePathMixed
		}) || slices.ContainsFunc(n.Facts.Registries, func(r RegistryMirrorFact) bool { return r.OverridePathMixed || len(r.Endpoints) == 0 }) {
			return nil
		}
	}

	out := make([]ichorv1alpha1.TalosClusterRegistry, 0, len(want))
	for _, r := range want {
		out = append(out, ichorv1alpha1.TalosClusterRegistry{
			Host: r.Host, Endpoints: r.Endpoints, SkipFallback: r.SkipFallback, OverridePath: r.OverridePath,
		})
	}

	return out
}

// readNodes are the nodes whose configuration adopt read.
func readNodes(nodes []*adoptNode) []*adoptNode {
	return slices.DeleteFunc(slices.Clone(nodes), func(n *adoptNode) bool { return n.Skipped != "" || n.Facts == nil })
}

const (
	endpointVIP          = "vip"
	endpointLoadBalancer = "loadBalancer"
	endpointNode         = "node"
)

func kubernetesVersionOr(v string) string {
	if v == "" {
		return release.Pinned.Kubernetes
	}

	if v[0] != 'v' {
		return "v" + v
	}

	return v
}
