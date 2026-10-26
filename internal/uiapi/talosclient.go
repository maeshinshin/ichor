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
	"time"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// NodeTalosClient is the Talos API surface GetNode, GetEffectiveConfig and
// StreamNodeLogs need from one machine. It is read-only by construction --
// there is no method that could apply, reboot or reset anything -- matching
// architecture.md's "Trust": "the UI backend holds a read-only [Talos
// identity]". NewMachineryNodeTalosClient (talosclient_machinery.go) is the
// real implementation; tests use a fake, the same unit-test rule
// internal/controller's TalosClient/MaintenanceClient follow (no Talos call
// in a test ever reaches a real node).
//
// The returned message types are the wire types themselves
// (gen/go/ichor/v1alpha1), not a parallel domain struct: nothing else in
// this package needs a Talos-shaped value that isn't about to be put
// straight on a GetNodeResponse, so a conversion layer would only have
// copied fields one by one for no reader.
type NodeTalosClient interface {
	// Hardware reads the machine's SMBIOS/CPU/memory facts.
	Hardware(ctx context.Context) (*pb.Hardware, error)

	// Disks lists the machine's block devices. Every disk's Role comes back
	// DISK_ROLE_UNSPECIFIED: disk role assignment
	// (TalosMachineSpec.spec.disks[]) does not exist on the CRD yet (ADR
	// 0016's note on ApproveMachineRequest applies here too), so there is no
	// source of truth to fill it from.
	Disks(ctx context.Context) ([]*pb.Disk, error)

	// ResourceUsage reads a live CPU/memory/filesystem snapshot.
	ResourceUsage(ctx context.Context) (*pb.ResourceUsage, error)

	// TalosSystem, TalosNetwork, TalosStorage, TalosServices and
	// TalosExtensions back GetNodeTalosDetails (docs/adr/0071). Each reads
	// one independent slice of the node, picking fields explicitly; none
	// returns a configuration document, certificate, key or token. The
	// section's Error field is left empty here: the caller fills it from the
	// returned error.
	TalosSystem(ctx context.Context) (*pb.TalosSystemSection, error)
	TalosNetwork(ctx context.Context) (*pb.TalosNetworkSection, error)
	TalosStorage(ctx context.Context) (*pb.TalosStorageSection, error)
	TalosServices(ctx context.Context) (*pb.TalosServicesSection, error)
	TalosExtensions(ctx context.Context) (*pb.TalosExtensionsSection, error)

	// TalosEtcd, TalosProcesses, TalosContainers, TalosNetworkState and
	// TalosKernelModules back GetNodeTalosRuntime (docs/adr/0082), on the
	// same terms as the methods above.
	TalosEtcd(ctx context.Context) (*pb.TalosEtcdSection, error)
	TalosProcesses(ctx context.Context) (*pb.TalosProcessesSection, error)
	TalosContainers(ctx context.Context) (*pb.TalosContainersSection, error)
	TalosNetworkState(ctx context.Context) (*pb.TalosNetworkStateSection, error)
	TalosKernelModules(ctx context.Context) (*pb.TalosKernelModulesSection, error)

	// StreamLogs tails one log and calls onLine for every line after since
	// (the mechanism StreamNodeLogs's resume token relies on) until ctx is
	// done or the underlying stream ends. service selects which log:
	// empty means the machine's kernel log (Talos's Dmesg API); any other
	// value is a Talos service name (e.g. "kubelet", "etcd", "trustd",
	// "apid", "machined"), read through Talos's Logs API in the "system"
	// containerd namespace -- the same namespace/driver talosctl's own
	// `logs <service>` uses by default (docs/adr/0018).
	//
	// Both APIs are confirmed readable by a role.Reader identity (same
	// role set as role.Admin/role.Operator, per
	// internal/app/machined/pkg/system/services/machined.go's RBAC table
	// in the vendored Talos source) -- this answers architecture.md's open
	// question "Can os:reader read logs?": yes. A PermissionDenied from
	// Talos regardless is surfaced as connect.CodePermissionDenied, not
	// swallowed, so a future Talos version narrowing that table fails
	// loudly instead of silently returning nothing.
	StreamLogs(ctx context.Context, service string, since time.Time, opts LogOptions, onLine func(LogLine) error) error
}

// Tail bounds for StreamNodeLogs (docs/adr/0079): a request that names no
// tail_lines gets DefaultTailLines, and no request gets more than
// MaxTailLines unless it asks for the whole history with a negative value.
const (
	DefaultTailLines = 500
	MaxTailLines     = 10000
)

// LogOptions says where StreamLogs starts and whether it keeps going.
// TailLines is the number of most recent lines to start from; negative
// means the whole history. Follow keeps the stream open for new lines.
type LogOptions struct {
	Follow    bool
	TailLines int32
}

// LogLine is one line NodeTalosClient.StreamLogs delivers. Time is when the
// backend observed the line, not a timestamp parsed out of it: Dmesg's own
// lines carry a monotonic boot-relative offset, not wall-clock time, so the
// backend's own clock is the only stable ordering key across a resume
// (docs/adr/0018).
type LogLine struct {
	Time    time.Time
	Message string
}
