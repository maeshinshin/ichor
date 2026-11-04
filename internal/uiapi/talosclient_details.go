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
	"fmt"
	"strings"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	talostime "github.com/siderolabs/talos/pkg/machinery/resources/time"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The methods below read the Talos API through COSI resources and the machine
// API's Version/ServiceList calls, all of which a role.Reader identity (and
// the insecure maintenance API, where it allows them) can use. Every method
// copies fields by name into a section message (talosdetails_map.go);
// nothing here reads a config, certificate, key or secrets resource.

// optional returns (zero, nil) when err is a COSI not-found error, so a
// resource a given Talos version or platform does not publish leaves its
// fields empty instead of failing the whole section.
func optional[T any](v T, err error) (T, error) {
	if state.IsNotFoundError(err) {
		var zero T

		return zero, nil
	}

	return v, err
}

func (t *machineryNodeTalosClient) TalosSystem(ctx context.Context) (*pb.TalosSystemSection, error) {
	out := &pb.TalosSystemSection{}

	ver, err := t.c.Version(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading version: %w", err)
	}

	if len(ver.GetMessages()) > 0 {
		msg := ver.GetMessages()[0]
		out.Version, out.Arch = msg.GetVersion().GetTag(), msg.GetVersion().GetArch()
		out.Platform, out.PlatformMode = msg.GetPlatform().GetName(), msg.GetPlatform().GetMode()
	}

	ms, err := optional(safe.StateGetByID[*runtime.MachineStatus](ctx, t.c.COSI, runtime.MachineStatusID))
	if err != nil {
		return nil, fmt.Errorf("reading machine status: %w", err)
	}

	if ms != nil {
		spec := ms.TypedSpec()
		out.Stage, out.Ready = spec.Stage.String(), spec.Status.Ready

		for _, c := range spec.Status.UnmetConditions {
			out.UnmetConditions = append(out.UnmetConditions, &pb.TalosUnmetCondition{Name: c.Name, Reason: c.Reason})
		}
	}

	hosts, err := safe.StateListAll[*network.HostnameStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("reading hostname: %w", err)
	}

	if hosts.Len() > 0 {
		out.Hostname, out.Domainname = hosts.Get(0).TypedSpec().Hostname, hosts.Get(0).TypedSpec().Domainname
	}

	nn, err := optional(safe.StateGetByID[*k8s.Nodename](ctx, t.c.COSI, k8s.NodenameID))
	if err != nil {
		return nil, fmt.Errorf("reading Kubernetes node name: %w", err)
	}

	if nn != nil {
		out.KubernetesNodename = nn.TypedSpec().Nodename
	}

	cmd, err := optional(safe.StateGetByID[*runtime.KernelCmdline](ctx, t.c.COSI, runtime.KernelCmdlineID))
	if err != nil {
		return nil, fmt.Errorf("reading kernel command line: %w", err)
	}

	cmdline := ""

	if cmd != nil {
		// Unredacted here: GetNodeTalosDetails masks credential-shaped
		// arguments for every NodeTalosClient.
		cmdline = cmd.TypedSpec().Cmdline
		out.KernelArgs = strings.Fields(cmdline)
	}

	// The boot security state; a node without one leaves the fields unset.
	ss, err := optional(safe.StateGetByID[*runtime.SecurityState](ctx, t.c.COSI, runtime.SecurityStateID))
	if err != nil {
		return nil, fmt.Errorf("reading security state: %w", err)
	}

	if ss != nil {
		securityFromSpec(out, ss.TypedSpec(), cmdline)
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosNetwork(ctx context.Context) (*pb.TalosNetworkSection, error) {
	out := &pb.TalosNetworkSection{}

	addrs, err := optional(safe.StateGetByID[*network.NodeAddress](ctx, t.c.COSI, network.NodeAddressDefaultID))
	if err != nil {
		return nil, fmt.Errorf("reading node addresses: %w", err)
	}

	if addrs != nil {
		for _, a := range addrs.TypedSpec().Addresses {
			out.Addresses = append(out.Addresses, a.String())
		}
	}

	links, err := safe.StateListAll[*network.LinkStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing links: %w", err)
	}

	for l := range links.All() {
		out.Links = append(out.Links, linkFromSpec(l.Metadata().ID(), l.TypedSpec()))
	}

	routes, err := safe.StateListAll[*network.RouteStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing routes: %w", err)
	}

	out.RouteCount = int32(routes.Len()) //nolint:gosec // route counts are small

	for r := range routes.All() {
		if spec := r.TypedSpec(); isDefaultRoute(spec.Destination, spec.Gateway) {
			out.DefaultGateways = append(out.DefaultGateways, spec.Gateway.String())
		}
	}

	resolvers, err := safe.StateListAll[*network.ResolverStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing resolvers: %w", err)
	}

	for r := range resolvers.All() {
		for _, s := range r.TypedSpec().NameServers {
			out.DnsServers = append(out.DnsServers, s.Addr.String())
		}

		out.SearchDomains = append(out.SearchDomains, r.TypedSpec().SearchDomains...)
	}

	servers, err := safe.StateListAll[*network.TimeServerStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing time servers: %w", err)
	}

	for s := range servers.All() {
		out.TimeServers = append(out.TimeServers, s.TypedSpec().NTPServers...)
	}

	ts, err := optional(safe.StateGetByID[*talostime.Status](ctx, t.c.COSI, talostime.StatusID))
	if err != nil {
		return nil, fmt.Errorf("reading time sync status: %w", err)
	}

	if ts != nil {
		out.TimeSynced, out.TimeSyncDisabled = ts.TypedSpec().Synced, ts.TypedSpec().SyncDisabled
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosStorage(ctx context.Context) (*pb.TalosStorageSection, error) {
	out := &pb.TalosStorageSection{}

	sys, err := optional(safe.StateGetByID[*block.SystemDisk](ctx, t.c.COSI, block.SystemDiskID))
	if err != nil {
		return nil, fmt.Errorf("reading system disk: %w", err)
	}

	var systemDiskID string
	if sys != nil {
		systemDiskID = sys.TypedSpec().DiskID
	}

	disks, err := safe.StateListAll[*block.Disk](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}

	for d := range disks.All() {
		out.Disks = append(out.Disks, diskFromSpec(d.Metadata().ID(), d.TypedSpec(), systemDiskID))
	}

	vols, err := safe.StateListAll[*block.VolumeStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	for v := range vols.All() {
		out.Volumes = append(out.Volumes, volumeFromSpec(v.Metadata().ID(), v.TypedSpec()))
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosServices(ctx context.Context) (*pb.TalosServicesSection, error) {
	resp, err := t.c.ServiceList(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	out := &pb.TalosServicesSection{}

	for _, m := range resp.GetMessages() {
		for _, s := range m.GetServices() {
			out.Services = append(out.Services, &pb.TalosService{
				Id: s.GetId(), State: s.GetState(),
				HealthUnknown: s.GetHealth().GetUnknown(), Healthy: s.GetHealth().GetHealthy(),
				HealthMessage: s.GetHealth().GetLastMessage(),
			})
		}
	}

	return out, nil
}

func (t *machineryNodeTalosClient) TalosExtensions(ctx context.Context) (*pb.TalosExtensionsSection, error) {
	exts, err := safe.StateListAll[*runtime.ExtensionStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing extensions: %w", err)
	}

	out := &pb.TalosExtensionsSection{}

	for e := range exts.All() {
		md := e.TypedSpec().Metadata
		out.Extensions = append(out.Extensions, &pb.TalosExtension{Name: md.Name, Version: md.Version, Author: md.Author})
	}

	return out, nil
}
