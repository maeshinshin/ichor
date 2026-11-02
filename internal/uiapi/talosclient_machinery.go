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
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/api/common"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/resources/block"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/perf"
	"google.golang.org/protobuf/types/known/emptypb"

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
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, id *TalosIdentity,
) (NodeTalosClient, error) {
	if id == nil {
		c, err := client.New(ctx, client.WithMaintenanceMode(m.DialEndpoint(), nil))
		if err != nil {
			return nil, fmt.Errorf("dialing maintenance Talos API: %w", err)
		}

		return &machineryNodeTalosClient{c: c}, nil
	}

	cfg := clientconfig.NewConfig(cluster.Name, []string{m.DialEndpoint()}, id.CA, &x509.PEMEncodedCertificateAndKey{Crt: id.Crt, Key: id.Key})

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

	specs := make([]*block.DiskSpec, 0, disks.Len())
	for i := range disks.Len() {
		specs = append(specs, disks.Get(i).TypedSpec())
	}

	return installableDisks(specs), nil
}

// installableDisks maps disk specs to pb.Disk, leaving out CD-ROM drives and
// read-only disks: neither can take an install or data role, so the approval
// pickers must not offer them. The hardware display (Talos details) lists
// block.Disk itself and still shows everything.
func installableDisks(specs []*block.DiskSpec) []*pb.Disk {
	out := make([]*pb.Disk, 0, len(specs))

	for _, spec := range specs {
		if spec.CDROM || spec.Readonly {
			continue
		}

		var byIDPath string

		for _, s := range spec.Symlinks {
			if strings.Contains(s, "/by-id/") {
				byIDPath = s

				break
			}
		}

		out = append(out, &pb.Disk{
			Wwid: spec.WWID, Serial: spec.Serial, ByIdPath: byIDPath, BusPath: spec.BusPath, DevPath: spec.DevPath,
			SizeBytes: int64(spec.Size), //nolint:gosec // disk sizes fit in int64
			// Role stays DISK_ROLE_UNSPECIFIED: disk role assignment does not
			// exist on the CRD yet (NodeTalosClient's own doc comment).
		})
	}

	return out
}

// ResourceUsage reads perf.Memory (kibibytes, as /proc/meminfo reports them),
// derives CPU use from two samples of perf.CPU (cumulative seconds per
// state, so a single sample says nothing about the present) and reads the
// data filesystem from the Mounts API.
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
		usage.MemoryTotalBytes, usage.MemoryUsedBytes = kibToBytes(spec.MemTotal), kibToBytes(spec.MemUsed)
	}

	if err := t.sampleCPU(ctx, usage); err != nil {
		return nil, err
	}

	// A refused or empty Mounts call leaves the filesystem fields at zero; the
	// UI shows that as unknown.
	if mounts, err := t.c.Mounts(ctx); err == nil {
		for _, m := range mounts.GetMessages() {
			usage.FilesystemUsedBytes, usage.FilesystemTotalBytes = dataFilesystem(m.GetStats())
		}
	}

	return usage, nil
}

// cpuSampleGap separates the two live reads of /proc/stat.
const cpuSampleGap = time.Second

// cpuReading is one cumulative CPU sample: the all-CPU total and the core count.
type cpuReading struct {
	Total perf.CPUStat
	Cores int
}

// readSystemStat reads /proc/stat live through MachineService.SystemStat. The perf.CPU resource
// is not used: Talos's perf controller rewrites it only every 30 seconds
// (internal/app/machined/pkg/controllers/perf: updateInterval), so two reads a few seconds
// apart return the same snapshot.
func (t *machineryNodeTalosClient) readSystemStat(ctx context.Context) (cpuReading, error) {
	resp, err := t.c.MachineClient.SystemStat(ctx, &emptypb.Empty{})
	if err != nil {
		return cpuReading{}, fmt.Errorf("reading cpu stats: %w", err)
	}

	msgs := resp.GetMessages()
	if len(msgs) == 0 || msgs[0].GetCpuTotal() == nil {
		return cpuReading{}, errors.New("reading cpu stats: empty SystemStat reply")
	}

	c := msgs[0].GetCpuTotal()

	return cpuReading{
		Total: perf.CPUStat{
			User: c.GetUser(), Nice: c.GetNice(), System: c.GetSystem(), Idle: c.GetIdle(),
			Iowait: c.GetIowait(), Irq: c.GetIrq(), SoftIrq: c.GetSoftIrq(), Steal: c.GetSteal(),
		},
		Cores: len(msgs[0].GetCpu()),
	}, nil
}

func (t *machineryNodeTalosClient) sampleCPU(ctx context.Context, usage *pb.ResourceUsage) error {
	return sampleCPUUsage(ctx, t.readSystemStat, cpuSampleGap, usage)
}

// sampleCPUUsage reads the counters twice, gap apart, and sets the cores in use from the
// difference. A failed read leaves the usage unsampled (shown as unknown) rather than failing the
// whole page.
func sampleCPUUsage(ctx context.Context, read func(context.Context) (cpuReading, error), gap time.Duration, usage *pb.ResourceUsage) error {
	first, err := read(ctx)
	if err != nil {
		return nil //nolint:nilerr // CPU is optional on the page
	}

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(gap):
	}

	next, err := read(ctx)
	if err != nil {
		return nil //nolint:nilerr // CPU is optional on the page
	}

	if cores, ok := cpuCoresUsed(first.Total, next.Total, next.Cores); ok {
		usage.CpuCoresUsed, usage.CpuSampled = cores, true
	}

	return nil
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
// cmd/talosctl/cmd/talos/{logs,image,root}.go in the vendored module). Both RPCs are confirmed readable
// by role.Reader, so a failure to open either is wrapped
// with that fact stated plainly, in case a future Talos version changes
// it.
//
// shortcut: Dmesg's lines are not timestamped in wall-clock time in the
// vendored module's resource shape (no controller source ships with it to
// check against), so every line's LogLine.Time is when this method
// observed it, not a time parsed from the line itself -- see LogLine's own
// doc comment on why that is still a valid resume key. Logs' lines (a
// container's stdout/stderr, framed by containerd) are not either.
func (t *machineryNodeTalosClient) StreamLogs(ctx context.Context, service string, since time.Time, opts LogOptions, onLine func(LogLine) error) error {
	var (
		stream dataStream
		err    error
		what   string
	)

	if service == "" {
		// shortcut: Dmesg only has a boolean tail, no line count, so a
		// snapshot reads the whole kernel ring and keeps the last TailLines
		// lines here; a followed stream keeps the boolean tail.
		stream, err = t.c.Dmesg(ctx, opts.Follow, opts.Follow)
		what = "kernel log"
	} else {
		stream, err = t.c.Logs(ctx, constants.SystemContainerdNamespace, common.ContainerDriver_CONTAINERD, service, opts.Follow, opts.TailLines)
		what = "service " + strconv.Quote(service) + "'s log"
	}

	if err != nil {
		return fmt.Errorf("opening %s with this backend's read-only (role.Reader) Talos identity: %w", what, err)
	}

	var pending bytes.Buffer

	// A kernel-log snapshot is trimmed to its last TailLines lines once the
	// ring has been read to the end.
	trim := service == "" && !opts.Follow && opts.TailLines > 0

	var ring []LogLine

	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil { //nolint:errorlint // grpc stream EOF is a sentinel, not a wrapped error
				if trim && ctx.Err() == nil {
					return sendLast(ring, int(opts.TailLines), onLine)
				}

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

			if trim {
				ring = append(ring, LogLine{Time: now, Message: line})

				continue
			}

			if err := onLine(LogLine{Time: now, Message: line}); err != nil {
				return err
			}
		}
	}
}

// sendLast delivers the last n of lines to onLine.
func sendLast(lines []LogLine, n int, onLine func(LogLine) error) error {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	for _, l := range lines {
		if err := onLine(l); err != nil {
			return err
		}
	}

	return nil
}
