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
	"slices"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
	configres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"google.golang.org/grpc"

	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// clusterProbeTimeout bounds the worker's "can its cluster endpoint be reached" check.
const clusterProbeTimeout = 5 * time.Second

// NodeCallTimeout bounds every call the inspection makes to the node's Talos API. A control plane
// whose etcd is not running answers some calls never (`talosctl etcd members` hangs against it), and
// an inspection that waits forever is worse than one that says the call did not answer.
const NodeCallTimeout = 20 * time.Second

// bounded runs call under a context that ends after d. A call that is cut off returns an error that
// says it did not answer within d, instead of a bare "context deadline exceeded".
func bounded[T any](
	ctx context.Context, d time.Duration, what string, call func(context.Context) (T, error),
) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	v, err := call(cctx)
	if err != nil && cctx.Err() != nil && ctx.Err() == nil {
		return v, fmt.Errorf("%s did not answer within %s", what, d)
	}

	return v, err
}

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

	return &machineryNode{c: c, address: address, timeout: NodeCallTimeout}, nil
}

type machineryNode struct {
	c       *client.Client
	address string
	// timeout bounds each call to the node.
	timeout time.Duration
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

	cfgRes, err := bounded(ctx, n.timeout, "the node's machine configuration",
		func(c context.Context) (*configres.MachineConfig, error) {
			return safe.StateGetByID[*configres.MachineConfig](c, n.c.COSI, configres.ActiveID)
		})
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

	if sys, err := bounded(ctx, n.timeout, "the node's hardware identity",
		func(c context.Context) (*hardware.SystemInformation, error) {
			return safe.StateGetByID[*hardware.SystemInformation](c, n.c.COSI, hardware.SystemInformationID)
		}); err == nil {
		in.UUID = sys.TypedSpec().UUID
	}

	if hn, err := bounded(ctx, n.timeout, "the node's hostname", func(c context.Context) (*network.HostnameStatus, error) {
		return safe.StateGetByID[*network.HostnameStatus](c, n.c.COSI, network.HostnameID)
	}); err == nil {
		in.Hostname = hn.TypedSpec().Hostname
	}

	if v, err := bounded(ctx, n.timeout, "the node's version", func(c context.Context) (*machine.VersionResponse, error) {
		return n.c.Version(c)
	}); err == nil {
		for _, msg := range v.GetMessages() {
			if tag := msg.GetVersion().GetTag(); tag != "" {
				in.Version = tag

				break
			}
		}
	}

	if in.ControlPlane {
		inspectEtcd(ctx, n.c, n.timeout, &in)
	} else if endpoint != "" {
		in.ClusterReachable = tcpReachable(ctx, endpoint)
	}

	in.Volumes, err = bounded(ctx, n.timeout, "the node's volume list", n.volumes)

	return in, err
}

// etcdAPI is the part of the Talos client the etcd inspection uses; a test supplies its own.
type etcdAPI interface {
	EtcdMemberList(
		ctx context.Context, req *machine.EtcdMemberListRequest, opts ...grpc.CallOption,
	) (*machine.EtcdMemberListResponse, error)
	EtcdStatus(ctx context.Context, opts ...grpc.CallOption) (*machine.EtcdStatusResponse, error)
}

// inspectEtcd reads a control plane's etcd membership into in, each call bounded by timeout. When the
// list cannot be read (etcd is not running, or does not answer) it sets in.EtcdProblem and nothing
// else: the cluster counts as not reachable, which Assess turns into a refusal of a graceful reset
// and, with --force-ungraceful, a warning.
func inspectEtcd(ctx context.Context, api etcdAPI, timeout time.Duration, in *Inspection) {
	resp, err := bounded(ctx, timeout, "etcd member list",
		func(c context.Context) (*machine.EtcdMemberListResponse, error) {
			return api.EtcdMemberList(c, &machine.EtcdMemberListRequest{QueryLocal: true})
		})
	if err != nil {
		in.EtcdProblem = err.Error()

		return
	}

	for _, msg := range resp.GetMessages() {
		for _, m := range msg.GetMembers() {
			in.EtcdMembers = append(in.EtcdMembers, EtcdMember{
				ID: m.GetId(), Hostname: m.GetHostname(), Unhealthy: !memberReachable(ctx, m.GetPeerUrls()),
			})
		}
	}

	markLocalMember(ctx, api, timeout, in)

	in.EtcdKnown = len(in.EtcdMembers) > 0
	in.ClusterReachable = in.EtcdKnown

	if !in.EtcdKnown {
		in.EtcdProblem = "etcd reports no members"
	}
}

// markLocalMember records the dialled node's own member ID and judges its
// health by its own etcd status (the Talos API reads only the node it is
// sent to). Other members are judged by whether their peer port answers, set
// by the caller. shortcut: a member whose peer port is firewalled from the
// controller counts as unhealthy, which errs toward refusing; --force overrides.
func markLocalMember(ctx context.Context, api etcdAPI, timeout time.Duration, in *Inspection) {
	resp, err := bounded(ctx, timeout, "etcd status", func(c context.Context) (*machine.EtcdStatusResponse, error) {
		return api.EtcdStatus(c)
	})
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

	return slices.ContainsFunc(peerURLs, func(raw string) bool {
		u, err := url.Parse(raw)

		return err == nil && u.Host != "" && tcpReachable(ctx, u.Host)
	})
}

// volumes lists what the reset leaves behind: user volumes (by their "u-" ID),
// and anything probed on a disk other than the system disk, except partition
// tables. shortcut: a volume on the system disk that is neither a user volume
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

		seen[s.Location] = true
		out = append(out, Volume{
			Name:      strings.TrimPrefix(vs.Metadata().ID(), constants.UserVolumePrefix),
			Location:  s.Location,
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

// UserDiskPaths lists the devices a system-disk-and-user-disks wipe names in UserDisksToWipe: every
// block.Disk except the system disk with the devices it is built on, CD-ROM drives and read-only
// disks (Talos refuses a reset that names a read-only disk). It fails when no system disk is known,
// since the user disks then cannot be told apart from it.
func UserDiskPaths(ctx context.Context, st state.State) ([]string, error) {
	system, err := blockres.GetSystemDiskDevicePaths(ctx, st)
	if err != nil {
		return nil, fmt.Errorf("finding the system disk: %w", err)
	}

	if len(system) == 0 {
		return nil, errors.New("no system disk is known, so the user disks cannot be told apart from it")
	}

	disks, err := safe.StateListAll[*blockres.Disk](ctx, st)
	if err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}

	var paths []string

	for i := range disks.Len() {
		spec := disks.Get(i).TypedSpec()
		if spec.CDROM || spec.Readonly || slices.Contains(system, spec.DevPath) {
			continue
		}

		paths = append(paths, spec.DevPath)
	}

	return paths, nil
}

// orphanCandidate reports whether a probed device is data on a disk the reset
// does not touch.
func orphanCandidate(s *blockres.DiscoveredVolumeSpec, systemDisk string) bool {
	if s.Type == "" || s.Type == "gpt" || s.Type == "dos" ||
		slices.ContainsFunc([]string{"/dev/loop", "/dev/sr", "/dev/zram", "/dev/ram", "/dev/dm-"},
			func(p string) bool { return strings.HasPrefix(s.DevPath, p) }) {
		return false
	}

	disk := cmp.Or(s.ParentDevPath, s.DevPath)

	return systemDisk != "" && disk != systemDisk
}

// HostOnly drops the port of address, if it has one.
var HostOnly = endpointguard.HostOf

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
var ErrOtherMachine = errors.New("another node now has the address")

// MaintenanceUUID dials address's Talos API in maintenance mode (no
// credential: a node with no configuration offers nothing else) and returns
// its SMBIOS UUID.
func MaintenanceUUID(ctx context.Context, address string) (string, error) {
	c, err := client.New(ctx, client.WithMaintenanceMode(address, nil))
	if err != nil {
		return "", err
	}

	defer c.Close() //nolint:errcheck

	sys, err := safe.StateGetByID[*hardware.SystemInformation](ctx, c.COSI, hardware.SystemInformationID)
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
