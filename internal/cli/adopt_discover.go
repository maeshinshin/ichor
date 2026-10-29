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
	"net/netip"
	"slices"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// adoptNode is one machine adopt looks at: the (possibly not yet written) TalosMachine plus what
// was read from the node.
type adoptNode struct {
	M *ichorv1alpha1.TalosMachine
	// Skipped is why adopt did not read the node (not approved).
	Skipped string

	Current []byte
	Facts   *TalosFacts
	// InstallDisk is the facts' install disk as the write phase takes it ("" if the node has no
	// plain device path).
	InstallDisk string
}

func (n *adoptNode) controlPlane() bool { return n.M.Spec.Role == roleControlPlane }

// discoverMachines builds the in-memory TalosMachines of a cluster Ichor has not seen yet, from
// the discovery membership of the seed node (1.4: the node list comes from membership,
// not from typing every address). Names are the hardware UUID, as the write phase will create
// them. Nothing is written.
func discoverMachines(ctx context.Context, deps adoptDeps, clusterName, namespace string) ([]*ichorv1alpha1.TalosMachine, error) {
	if deps.Seed == "" {
		return nil, fmt.Errorf("adopt: TalosCluster %q does not exist yet; give --node, the address of one control plane, "+
			"so adopt can discover the cluster's machines", clusterName)
	}

	if deps.ReadIdentity == nil {
		return nil, errors.New("adopt: no way to read the nodes' hardware identity was given")
	}

	seed, err := deps.DialTalos(ctx, deps.Seed)
	if err != nil {
		return nil, fmt.Errorf("adopt: dialing %q: %w", deps.Seed, err)
	}

	members, ok, err := ReadClusterMembers(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("adopt: listing the cluster members: %w", err)
	}

	if !ok || len(members) == 0 {
		members = []ClusterMember{{Addresses: []string{deps.Seed}}}
	}

	machines := make([]*ichorv1alpha1.TalosMachine, 0, len(members))

	for _, mem := range members {
		if len(mem.Addresses) == 0 {
			return nil, fmt.Errorf("adopt: cluster member %q reports no address", mem.Hostname)
		}

		addr := mem.Addresses[0]

		id, err := deps.ReadIdentity(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("adopt: reading the hardware identity of %q: %w", addr, err)
		}

		if id.UUID == "" {
			return nil, fmt.Errorf("adopt: the node at %q reported no SMBIOS UUID; adopt needs a strong hardware identity", addr)
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

	return machines, nil
}

// completeNode fills what only the node's configuration says: the role when membership gave none,
// and the install disk.
func completeNode(n *adoptNode) error {
	if n.M.Spec.Role == "" {
		n.M.Spec.Role = n.Facts.Role
	}

	if n.M.Spec.Install != nil || n.Facts.Install.Disk == "" {
		return nil
	}

	disk, err := parseInstallDisk(n.Facts.Install.Disk)
	if err != nil {
		return fmt.Errorf("adopt: node %q: %w", n.M.Spec.Endpoint, err)
	}

	n.InstallDisk = n.Facts.Install.Disk
	n.M.Spec.Install = &ichorv1alpha1.TalosMachineInstall{Disk: *disk}

	return nil
}

// clusterFromNodes builds the TalosCluster adopt will create from what the nodes' configurations
// say. The CNI fields and the version contract are set afterwards, by the inspection and the
// probing.
func clusterFromNodes(name, namespace, talosVersion string, nodes []*adoptNode) (*ichorv1alpha1.TalosCluster, error) {
	var (
		cp      *adoptNode
		cpCount int32
		workers int
	)

	for _, n := range nodes {
		switch {
		case n.Skipped != "":
		case n.controlPlane():
			cpCount++

			if cp == nil {
				cp = n
			}
		default:
			workers++
		}
	}

	if cp == nil {
		return nil, errors.New("adopt: no control plane among the cluster's machines")
	}

	f := cp.Facts

	endpointType := "dns"

	if addr, err := netip.ParseAddr(f.EndpointHost); err == nil {
		endpointType = "vip"

		if slices.ContainsFunc(nodes, func(n *adoptNode) bool { return n.M.Spec.Endpoint == addr.String() }) {
			endpointType = "node"
		}
	}

	if f.EndpointHost == "" {
		return nil, errors.New("adopt: the control plane's configuration names no cluster endpoint")
	}

	schedulable := "true"
	if f.ScheduleOnControlPlanes != nil {
		schedulable = fmt.Sprint(*f.ScheduleOnControlPlanes)
	} else if workers > 0 {
		schedulable = "false"
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

	if cands := contractCandidates(talosVersion); len(cands) > 0 {
		c.Spec.VersionContract = cands[0]
	}

	return c, nil
}

func kubernetesVersionOr(v string) string {
	if v == "" {
		return "v" + constants.DefaultKubernetesVersion
	}

	if v[0] != 'v' {
		return "v" + v
	}

	return v
}
