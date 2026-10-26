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

package uiapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	"github.com/siderolabs/talos/pkg/machinery/resources/kubespan"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	talostime "github.com/siderolabs/talos/pkg/machinery/resources/time"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The methods below back GetNodeTalosRuntime. All are reads
// (list/get calls and COSI gets); each copies fields by name.

func (t *machineryNodeTalosClient) TalosEtcd(ctx context.Context) (*pb.TalosEtcdSection, error) {
	out := &pb.TalosEtcdSection{}

	st, err := t.c.EtcdStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading etcd status: %w", err)
	}

	var leader uint64

	if msgs := st.GetMessages(); len(msgs) > 0 && msgs[0].GetMemberStatus() != nil {
		ms := msgs[0].GetMemberStatus()
		leader = ms.GetLeader()
		out.MemberId, out.LeaderId = memberID(ms.GetMemberId()), memberID(leader)
		out.DbSizeBytes, out.DbSizeInUseBytes = ms.GetDbSize(), ms.GetDbSizeInUse()
		out.RaftIndex, out.RaftTerm, out.RaftAppliedIndex = ms.GetRaftIndex(), ms.GetRaftTerm(), ms.GetRaftAppliedIndex()
		out.ProtocolVersion, out.StorageVersion = ms.GetProtocolVersion(), ms.GetStorageVersion()
		out.StatusErrors = ms.GetErrors()
	}

	members, err := t.c.EtcdMemberList(ctx, &machine.EtcdMemberListRequest{})
	if err != nil {
		return nil, fmt.Errorf("listing etcd members: %w", err)
	}

	for _, msg := range members.GetMessages() {
		for _, m := range msg.GetMembers() {
			out.Members = append(out.Members, &pb.TalosEtcdMember{
				Id: memberID(m.GetId()), Hostname: m.GetHostname(), PeerUrls: m.GetPeerUrls(),
				ClientUrls: m.GetClientUrls(), Learner: m.GetIsLearner(), Leader: leader != 0 && m.GetId() == leader,
			})
		}
	}

	alarms, err := t.c.EtcdAlarmList(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing etcd alarms: %w", err)
	}

	for _, msg := range alarms.GetMessages() {
		for _, a := range msg.GetMemberAlarms() {
			out.Alarms = append(out.Alarms, memberID(a.GetMemberId())+": "+a.GetAlarm().String())
		}
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosProcesses(ctx context.Context) (*pb.TalosProcessesSection, error) {
	resp, err := t.c.Processes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing processes: %w", err)
	}

	var all []*machine.ProcessInfo

	for _, m := range resp.GetMessages() {
		all = append(all, m.GetProcesses()...)
	}

	return &pb.TalosProcessesSection{Total: int32(len(all)), Processes: topProcesses(all)}, nil //nolint:gosec // process counts are small
}

func (t *machineryNodeTalosClient) TalosContainers(ctx context.Context) (*pb.TalosContainersSection, error) {
	out := &pb.TalosContainersSection{}

	var errs []error

	for _, src := range []struct {
		ns     string
		driver common.ContainerDriver
	}{{"k8s.io", common.ContainerDriver_CRI}, {"system", common.ContainerDriver_CONTAINERD}} {
		resp, err := t.c.Containers(ctx, src.ns, src.driver)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing %s containers: %w", src.ns, err))

			continue
		}

		for _, m := range resp.GetMessages() {
			for _, c := range m.GetContainers() {
				if len(out.Containers) >= maxContainers {
					out.Truncated = true

					return out, nil
				}

				out.Containers = append(out.Containers, containerFromInfo(c))
			}
		}
	}

	// One runtime being down (kubelet not started yet) still shows the other.
	if len(errs) == 2 {
		return nil, errors.Join(errs...)
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosNetworkState(ctx context.Context) (*pb.TalosNetworkStateSection, error) {
	out := &pb.TalosNetworkStateSection{}

	routes, err := safe.StateListAll[*network.RouteStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing routes: %w", err)
	}

	for r := range routes.All() {
		if len(out.Routes) >= maxRoutes {
			out.RoutesTruncated = true

			break
		}

		s := r.TypedSpec()
		out.Routes = append(out.Routes, &pb.TalosRoute{
			Family: s.Family.String(), Destination: s.Destination.String(), Gateway: addrString(s.Gateway),
			Source: addrString(s.Source), Link: s.OutLinkName, Table: s.Table.String(), Protocol: s.Protocol.String(),
			Scope: s.Scope.String(), Type: s.Type.String(), Priority: s.Priority,
		})
	}

	members, err := safe.StateListAll[*cluster.Member](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing discovery members: %w", err)
	}

	for m := range members.All() {
		s := m.TypedSpec()
		dm := &pb.TalosDiscoveryMember{Hostname: s.Hostname, MachineType: s.MachineType.String(), OperatingSystem: s.OperatingSystem}

		for _, a := range s.Addresses {
			dm.Addresses = append(dm.Addresses, a.String())
		}

		out.Members = append(out.Members, dm)
	}

	// Peers are listed without their public keys: the key is the resource ID
	// and is not copied.
	peers, err := safe.StateListAll[*kubespan.PeerStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing KubeSpan peers: %w", err)
	}

	for p := range peers.All() {
		s := p.TypedSpec()
		peer := &pb.TalosKubeSpanPeer{
			Label: s.Label, Endpoint: s.Endpoint.String(), State: s.State.String(),
			ReceiveBytes: s.ReceiveBytes, TransmitBytes: s.TransmitBytes,
		}

		if !s.LastHandshakeTime.IsZero() {
			peer.LastHandshakeUnix = s.LastHandshakeTime.Unix()
		}

		out.KubespanPeers = append(out.KubespanPeers, peer)
	}

	adj, err := optional(safe.StateGetByID[*talostime.AdjtimeStatus](ctx, t.c.COSI, talostime.AdjtimeStatusID))
	if err != nil {
		return nil, fmt.Errorf("reading time adjustment: %w", err)
	}

	if adj != nil {
		s := adj.TypedSpec()
		out.TimeStatus, out.TimeSync = s.State, s.SyncStatus
		out.TimeOffsetNanos, out.TimeMaxErrorNanos = int64(s.Offset), int64(s.MaxError)
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosKernelModules(ctx context.Context) (*pb.TalosKernelModulesSection, error) {
	mods, err := safe.StateListAll[*runtime.LoadedKernelModule](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing kernel modules: %w", err)
	}

	out := &pb.TalosKernelModulesSection{}

	for m := range mods.All() {
		s := m.TypedSpec()
		out.Modules = append(out.Modules, &pb.TalosKernelModule{
			Name: m.Metadata().ID(), SizeBytes: int64(s.Size), ReferenceCount: int32(s.ReferenceCount), State: s.State, //nolint:gosec // module sizes and counts are small
		})
	}

	return out, nil
}
