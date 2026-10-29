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

package reclaim

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
)

// clusterProbeTimeout bounds the worker's "can its cluster endpoint be reached" check.
const clusterProbeTimeout = 5 * time.Second

// Dial connects to the single node at address with the operator's credential.
//
// Only address is dialled. The talosconfig's own endpoints (and any nodes it
// lists) are ignored, so a multi-node talosconfig cannot widen the scope of a
// reset. contextName picks the talosconfig context ("" is the file's default).
// Errors never contain the credential.
func Dial(ctx context.Context, cred Credential, address, contextName string) (Node, error) {
	cfg, err := clientconfig.FromBytes(cred.Bytes())
	if err != nil {
		// The parse error can quote the input; say only that it failed.
		return nil, errors.New("the talosconfig could not be parsed as a Talos client configuration")
	}

	opts := []client.OptionFunc{client.WithConfig(cfg), client.WithEndpoints(address)}
	if contextName != "" {
		opts = append(opts, client.WithContextName(contextName))
	}

	c, err := client.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("dialing the Talos API at %q: %w", address, err)
	}

	return &machineryNode{c: c, address: address}, nil
}

type machineryNode struct {
	c       *client.Client
	address string
}

func (n *machineryNode) Close() error { return n.c.Close() }

// Reset sends ResetRequest(graceful), the only reset Ichor sends.
func (n *machineryNode) Reset(ctx context.Context, graceful bool) error {
	return n.c.ResetGeneric(ctx, ResetRequest(graceful))
}

// Inspect reads only: machine config (type, cluster name and ID), SMBIOS UUID,
// hostname, version, etcd members, and the volumes on the node.
func (n *machineryNode) Inspect(ctx context.Context) (Inspection, error) {
	in := Inspection{Address: n.address}

	cfgRes, err := safe.StateGet[*configres.MachineConfig](ctx, n.c.COSI,
		resource.NewMetadata(configres.NamespaceName, configres.MachineConfigType, configres.ActiveID,
			resource.VersionUndefined))
	if err != nil {
		return in, fmt.Errorf("reading the machine configuration (configured node? admin credential?): %w", err)
	}

	cfg := cfgRes.Provider()
	if m := cfg.Machine(); m != nil {
		in.ControlPlane = m.Type().IsControlPlane()
	}

	var endpoint string

	if k := cfg.K8sClusterConfig(); k != nil {
		in.ClusterName = k.ClusterName()
		if u := k.ClusterEndpoint(); u != nil {
			endpoint = u.Host
		}
	}

	if d := cfg.DiscoveryIdentityConfig(); d != nil {
		in.ClusterID = d.ClusterID()
	}

	if sys, err := safe.StateGet[*hardware.SystemInformation](ctx, n.c.COSI, resource.NewMetadata(
		hardware.NamespaceName, hardware.SystemInformationType, hardware.SystemInformationID,
		resource.VersionUndefined)); err == nil {
		in.UUID = sys.TypedSpec().UUID
	}

	if hn, err := safe.StateGet[*network.HostnameStatus](ctx, n.c.COSI, resource.NewMetadata(
		network.NamespaceName, network.HostnameStatusType, network.HostnameID, resource.VersionUndefined)); err == nil {
		in.Hostname = hn.TypedSpec().Hostname
	}

	if v, err := n.c.Version(ctx); err == nil {
		for _, msg := range v.GetMessages() {
			if tag := msg.GetVersion().GetTag(); tag != "" {
				in.Version = tag

				break
			}
		}
	}

	if in.ControlPlane {
		resp, err := n.c.EtcdMemberList(ctx, &machine.EtcdMemberListRequest{QueryLocal: true})
		if err == nil {
			for _, msg := range resp.GetMessages() {
				for _, m := range msg.GetMembers() {
					in.EtcdMembers = append(in.EtcdMembers, EtcdMember{
						ID: m.GetId(), Hostname: m.GetHostname(), Unhealthy: !memberReachable(ctx, m.GetPeerUrls()),
					})
				}
			}

			n.markLocalMember(ctx, &in)

			in.EtcdKnown = len(in.EtcdMembers) > 0
			in.ClusterReachable = in.EtcdKnown
		}
	} else if endpoint != "" {
		in.ClusterReachable = tcpReachable(ctx, endpoint)
	}

	in.Volumes, err = n.volumes(ctx)

	return in, err
}

// markLocalMember records the dialled node's own member ID and judges its
// health by its own etcd status (the Talos API reads only the node it is
// sent to). Other members are judged by whether their peer port answers, set
// by the caller. ponytail: a member whose peer port is firewalled from the
// controller counts as unhealthy, which errs toward refusing; --force overrides.
func (n *machineryNode) markLocalMember(ctx context.Context, in *Inspection) {
	resp, err := n.c.EtcdStatus(ctx)
	if err != nil {
		return
	}

	for _, msg := range resp.GetMessages() {
		st := msg.GetMemberStatus()
		if st == nil {
			continue
		}

		in.LocalMemberID = st.GetMemberId()

		for i := range in.EtcdMembers {
			if in.EtcdMembers[i].ID == st.GetMemberId() {
				in.EtcdMembers[i].Unhealthy = len(st.GetErrors()) > 0 || st.GetLeader() == 0
			}
		}

		return
	}
}

// memberReachable is true when any of the member's peer URLs accepts a TCP
// connection, or when it lists none (nothing to judge by).
func memberReachable(ctx context.Context, peerURLs []string) bool {
	if len(peerURLs) == 0 {
		return true
	}

	for _, raw := range peerURLs {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}

		if tcpReachable(ctx, u.Host) {
			return true
		}
	}

	return false
}

// volumes lists what the reset leaves behind: user volumes (by their "u-" ID),
// and anything probed on a disk other than the system disk, except partition
// tables. ponytail: a volume on the system disk that is neither a user volume
// nor Talos's own is not listed; Talos's own partitions are the only thing the
// installer puts there.
func (n *machineryNode) volumes(ctx context.Context) ([]Volume, error) {
	var systemDisk string

	if sd, err := blockres.GetSystemDisk(ctx, n.c.COSI); err == nil && sd != nil {
		systemDisk = sd.DevPath
	}

	seen := map[string]bool{}

	var out []Volume

	statuses, err := safe.StateListAll[*blockres.VolumeStatus](ctx, n.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing volume status: %w", err)
	}

	for vs := range statuses.All() {
		if !strings.HasPrefix(vs.Metadata().ID(), constants.UserVolumePrefix) {
			continue
		}

		s := vs.TypedSpec()
		loc := s.Location

		seen[loc] = true
		out = append(out, Volume{
			Name:      strings.TrimPrefix(vs.Metadata().ID(), constants.UserVolumePrefix),
			Location:  loc,
			Size:      s.Size,
			Encrypted: s.EncryptionProvider != blockres.EncryptionProviderNone,
		})
	}

	discovered, err := safe.StateListAll[*blockres.DiscoveredVolume](ctx, n.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing discovered volumes: %w", err)
	}

	for dv := range discovered.All() {
		s := dv.TypedSpec()
		if !orphanCandidate(s, systemDisk) || seen[s.DevPath] {
			continue
		}

		seen[s.DevPath] = true
		out = append(out, Volume{
			Name: cmp.Or(s.Label, s.Name, s.DevPath), Location: s.DevPath, Size: s.Size, Encrypted: s.Type == "luks2",
		})
	}

	return out, nil
}

// orphanCandidate reports whether a probed device is data on a disk the reset
// does not touch.
func orphanCandidate(s *blockres.DiscoveredVolumeSpec, systemDisk string) bool {
	if s.Type == "" || s.Type == "gpt" || s.Type == "dos" {
		return false
	}

	for _, p := range []string{"/dev/loop", "/dev/sr", "/dev/zram", "/dev/ram", "/dev/dm-"} {
		if strings.HasPrefix(s.DevPath, p) {
			return false
		}
	}

	disk := cmp.Or(s.ParentDevPath, s.DevPath)

	return systemDisk != "" && disk != systemDisk
}

// HostOnly drops the port of address, if it has one.
func HostOnly(address string) string {
	if h, _, err := net.SplitHostPort(address); err == nil {
		return h
	}

	return address
}

func tcpReachable(ctx context.Context, hostport string) bool {
	if _, _, err := net.SplitHostPort(hostport); err != nil {
		hostport = net.JoinHostPort(hostport, "443")
	}

	d := net.Dialer{Timeout: clusterProbeTimeout}

	c, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return false
	}

	_ = c.Close()

	return true
}

// ErrOtherMachine is returned (wrapped) by WaitMaintenance when the address
// answers in maintenance mode with a different UUID: another machine now has it.
var ErrOtherMachine = errors.New("another machine now has the address")

// MaintenanceUUID dials address's Talos API in maintenance mode (no
// credential: a node with no configuration offers nothing else) and returns
// its SMBIOS UUID.
func MaintenanceUUID(ctx context.Context, address string) (string, error) {
	c, err := client.New(ctx, client.WithMaintenanceMode(address, nil))
	if err != nil {
		return "", err
	}

	defer c.Close() //nolint:errcheck

	sys, err := safe.StateGet[*hardware.SystemInformation](ctx, c.COSI, resource.NewMetadata(
		hardware.NamespaceName, hardware.SystemInformationType, hardware.SystemInformationID, resource.VersionUndefined))
	if err != nil {
		return "", err
	}

	return sys.TypedSpec().UUID, nil
}

// WaitMaintenance polls probe(address) until it returns uuid (the node is back
// in maintenance mode with the same identity) or ctx ends. probe is
// MaintenanceUUID in production. interval is the pause between polls. The
// error of the last probe is kept so that a timeout says why.
func WaitMaintenance(
	ctx context.Context, address, uuid string, interval time.Duration,
	probe func(ctx context.Context, address string) (string, error),
) error {
	var last error

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		got, err := probe(pctx, address)

		cancel()

		switch {
		case err == nil && strings.EqualFold(got, uuid):
			return nil
		case err == nil:
			return fmt.Errorf("%s answers in maintenance mode with UUID %s, not %s: %w",
				address, got, uuid, ErrOtherMachine)
		default:
			last = err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s to return to maintenance mode: %w (last probe: %v)", address, ctx.Err(), last)
		case <-t.C:
		}
	}
}
