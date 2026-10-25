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

	// NetworkInterfaces lists the machine's physical links and their
	// addresses. Alias comes back empty until LinkAliasConfig assignment
	// exists (same gap as Disks' Role).
	NetworkInterfaces(ctx context.Context) ([]*pb.NetworkInterface, error)

	// ResourceUsage reads a live CPU/memory/filesystem snapshot.
	ResourceUsage(ctx context.Context) (*pb.ResourceUsage, error)

	// StreamLogs tails the machine's kernel log (Talos's Dmesg API) --
	// StreamNodeLogsRequest carries no service/container selector, so the
	// whole-machine system log is the only log this RPC's shape can mean --
	// calling onLine for every line until ctx is done or ReadLogs returns.
	// since drops any line observed at or before it, the mechanism
	// StreamNodeLogs's resume token relies on.
	StreamLogs(ctx context.Context, since time.Time, onLine func(LogLine) error) error
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
