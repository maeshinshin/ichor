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
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/network"
	"github.com/siderolabs/talos/pkg/machinery/resources/perf"
	"github.com/siderolabs/talos/pkg/machinery/role"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// NewMachineryNodeTalosClient dials m's Talos API read-only and adapts the
// connection to NodeTalosClient. It is what cmd/ichor-ui wires into
// Server.NodeTalosClient.
//
// bundle nil means m has not been approved yet, so no CA trust exists for
// it: the only connection a node in that state offers is maintenance
// mode's insecure, unauthenticated one (architecture.md "Nodes"), the same
// path internal/controller.NewMachineryMaintenanceClient uses for
// discovery. bundle non-nil mints a role.Reader client certificate --
// "readers who can access read-only APIs that do not expose secrets"
// (pkg/machinery/role's own doc comment) -- never role.Admin, matching
// "Trust": "the UI backend holds a read-only [Talos] identity."
func NewMachineryNodeTalosClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (NodeTalosClient, error) {
	if bundle == nil {
		c, err := client.New(ctx, client.WithMaintenanceMode(m.Spec.Endpoint, nil))
		if err != nil {
			return nil, fmt.Errorf("dialing maintenance Talos API: %w", err)
		}

		return &machineryNodeTalosClient{c: c}, nil
	}

	cert, err := bundle.GenerateTalosAPIClientCertificate(role.MakeSet(role.Reader))
	if err != nil {
		return nil, fmt.Errorf("generating reader client certificate: %w", err)
	}

	cfg := clientconfig.NewConfig(cluster.Name, []string{m.Spec.Endpoint}, bundle.Certs.OS.Crt, cert)

	c, err := client.New(ctx, client.WithConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("dialing Talos API: %w", err)
	}

	return &machineryNodeTalosClient{c: c}, nil
}

// machineryNodeTalosClient adapts a real *client.Client to NodeTalosClient.
type machineryNodeTalosClient struct {
	c *client.Client
}

// Close releases the gRPC connection, through the same io.Closer type
// assertion internal/controller's machinery clients use.
func (t *machineryNodeTalosClient) Close() error {
	return t.c.Close()
}

// Hardware reads hardware.SystemInformation for the SMBIOS facts and sums
// hardware.Processor/MemoryModule for core and memory totals. A missing
// SystemInformation resource (not every platform reports SMBIOS data, the
// same case internal/controller.machineryMaintenanceClient.Identity
// handles) leaves those three fields empty rather than failing the call.
func (t *machineryNodeTalosClient) Hardware(ctx context.Context) (*pb.Hardware, error) {
	hw := &pb.Hardware{}

	sysInfo, err := safe.StateGet[*hardware.SystemInformation](ctx, t.c.COSI, resource.NewMetadata(
		hardware.NamespaceName, hardware.SystemInformationType, hardware.SystemInformationID, resource.VersionUndefined,
	))

	switch {
	case err == nil:
		spec := sysInfo.TypedSpec()
		hw.Manufacturer, hw.Model, hw.SerialNumber = spec.Manufacturer, spec.ProductName, spec.SerialNumber
	case state.IsNotFoundError(err):
		// No SMBIOS data on this platform; leave the three fields empty.
	default:
		return nil, fmt.Errorf("reading system information: %w", err)
	}

	processors, err := safe.StateListAll[*hardware.Processor](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing processors: %w", err)
	}

	for i := range processors.Len() {
		hw.CpuCores += int32(processors.Get(i).TypedSpec().CoreCount) //nolint:gosec // CoreCount is a small hardware count
	}

	modules, err := safe.StateListAll[*hardware.MemoryModule](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing memory modules: %w", err)
	}

	for i := range modules.Len() {
		hw.MemoryTotalBytes += int64(modules.Get(i).TypedSpec().Size) * 1024 * 1024 // Size is MiB.
	}

	return hw, nil
}

// Disks lists block.Disk, mapping architecture.md's identification
// fallback chain (WWID, serial, by-id path, bus path) directly: by-id path
// is derived from Symlinks, since block.Disk carries no dedicated field for
// it (confirmed by reading the vendored resources/block/disk.go).
func (t *machineryNodeTalosClient) Disks(ctx context.Context) ([]*pb.Disk, error) {
	disks, err := safe.StateListAll[*block.Disk](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing disks: %w", err)
	}

	out := make([]*pb.Disk, 0, disks.Len())

	for i := range disks.Len() {
		spec := disks.Get(i).TypedSpec()

		var byIDPath string

		for _, s := range spec.Symlinks {
			if strings.Contains(s, "/by-id/") {
				byIDPath = s

				break
			}
		}

		out = append(out, &pb.Disk{
			Wwid: spec.WWID, Serial: spec.Serial, ByIdPath: byIDPath, BusPath: spec.BusPath,
			SizeBytes: int64(spec.Size), //nolint:gosec // disk sizes fit in int64
			// Role stays DISK_ROLE_UNSPECIFIED: disk role assignment does not
			// exist on the CRD yet (NodeTalosClient's own doc comment).
		})
	}

	return out, nil
}

// NetworkInterfaces lists network.LinkStatus for physical links only
// (Physical() excludes virtual/loopback links, the same filter
// internal/controller.machineryMaintenanceClient.Identity applies), joined
// with network.AddressStatus for each link's addresses.
func (t *machineryNodeTalosClient) NetworkInterfaces(ctx context.Context) ([]*pb.NetworkInterface, error) {
	links, err := safe.StateListAll[*network.LinkStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing link status: %w", err)
	}

	addrs, err := safe.StateListAll[*network.AddressStatus](ctx, t.c.COSI)
	if err != nil {
		return nil, fmt.Errorf("listing address status: %w", err)
	}

	out := make([]*pb.NetworkInterface, 0, links.Len())

	for i := range links.Len() {
		link := links.Get(i)
		spec := link.TypedSpec()

		if !spec.Physical() {
			continue
		}

		iface := &pb.NetworkInterface{
			Name: link.Metadata().ID(), Alias: spec.Alias,
			PermanentMac: spec.PermanentAddr.String(), Up: spec.LinkState,
		}

		for j := range addrs.Len() {
			if addrs.Get(j).TypedSpec().LinkName == iface.Name {
				iface.Addresses = append(iface.Addresses, addrs.Get(j).TypedSpec().Address.String())
			}
		}

		out = append(out, iface)
	}

	return out, nil
}

// ResourceUsage reads perf.Memory directly (already a point-in-time
// snapshot) and derives CPU utilization from perf.CPU's cumulative-total
// fields.
//
// shortcut: perf.CPUSpec's fields are not documented as a percentage or a
// cumulative jiffy counter anywhere in the vendored machinery module (no
// controller source ships with it, only the resource shape), so this
// treats CPUTotal as already being the latest sample window's percentages
// -- cpu_cores_used = (100-idle)/100 * cores -- which has not been checked
// against a real Talos node's live values. Upgrade by sampling perf.CPU
// twice and computing a rate once this is checked against a real cluster.
func (t *machineryNodeTalosClient) ResourceUsage(ctx context.Context) (*pb.ResourceUsage, error) {
	usage := &pb.ResourceUsage{}

	mem, err := safe.StateGet[*perf.Memory](ctx, t.c.COSI, resource.NewMetadata(
		perf.NamespaceName, perf.MemoryType, perf.MemoryID, resource.VersionUndefined,
	))
	if err != nil && !state.IsNotFoundError(err) {
		return nil, fmt.Errorf("reading memory stats: %w", err)
	}

	if err == nil {
		spec := mem.TypedSpec()
		usage.MemoryTotalBytes, usage.MemoryUsedBytes = int64(spec.MemTotal), int64(spec.MemUsed) //nolint:gosec
	}

	cpu, err := safe.StateGet[*perf.CPU](ctx, t.c.COSI, resource.NewMetadata(
		perf.NamespaceName, perf.CPUType, perf.CPUID, resource.VersionUndefined,
	))
	if err != nil && !state.IsNotFoundError(err) {
		return nil, fmt.Errorf("reading cpu stats: %w", err)
	}

	if err == nil {
		idle := cpu.TypedSpec().CPUTotal.Idle
		usage.CpuCoresUsed = (100 - idle) / 100 * float64(len(cpu.TypedSpec().CPU))
	}

	return usage, nil
}

// dataStream is the common shape of Dmesg's and Logs' response streams
// (both are grpc.ServerStreamingClient[common.Data] -- confirmed by reading
// api/machine/machine_grpc.pb.go in the vendored module), factored out so
// StreamLogs reads either one through the same loop.
type dataStream interface {
	Recv() (*common.Data, error)
}

// StreamLogs tails one log, calling onLine for every line after since.
// service empty means Talos's kernel log (Dmesg); any other value is a
// Talos service name, read through Logs in the "system" containerd
// namespace with the containerd driver -- the same namespace/driver
// `talosctl logs <service>` uses by default (confirmed by reading
// cmd/talosctl/cmd/talos/{logs,image,root}.go in the vendored module;
// docs/adr/0018 records the exact trace). Both RPCs are confirmed readable
// by role.Reader (docs/adr/0018), so a failure to open either is wrapped
// with that fact stated plainly, in case a future Talos version changes
// it.
//
// shortcut: Dmesg's lines are not timestamped in wall-clock time in the
// vendored module's resource shape (no controller source ships with it to
// check against), so every line's LogLine.Time is when this method
// observed it, not a time parsed from the line itself -- see LogLine's own
// doc comment on why that is still a valid resume key. Logs' lines (a
// container's stdout/stderr, framed by containerd) are not either.
func (t *machineryNodeTalosClient) StreamLogs(ctx context.Context, service string, since time.Time, onLine func(LogLine) error) error {
	var (
		stream dataStream
		err    error
		what   string
	)

	if service == "" {
		stream, err = t.c.Dmesg(ctx, true, true)
		what = "kernel log"
	} else {
		stream, err = t.c.Logs(ctx, constants.SystemContainerdNamespace, common.ContainerDriver_CONTAINERD, service, true, -1)
		what = "service " + strconv.Quote(service) + "'s log"
	}

	if err != nil {
		return fmt.Errorf("opening %s with this backend's read-only (role.Reader) Talos identity: %w", what, err)
	}

	var pending bytes.Buffer

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil { //nolint:errorlint // grpc stream EOF is a sentinel, not a wrapped error
				return nil
			}

			return fmt.Errorf("reading %s with this backend's read-only (role.Reader) Talos identity: %w", what, err)
		}

		pending.Write(resp.GetBytes())

		for {
			line, err := pending.ReadString('\n')
			if err != nil {
				// Incomplete line: put it back for the next chunk.
				pending.Reset()
				pending.WriteString(line)

				break
			}

			line = strings.TrimRight(line, "\n")
			if line == "" {
				continue
			}

			now := time.Now()
			if !now.After(since) {
				continue
			}

			if err := onLine(LogLine{Time: now, Message: line}); err != nil {
				return err
			}
		}
	}
}
