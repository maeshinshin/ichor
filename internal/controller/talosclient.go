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

package controller

import "context"

// TalosClient is the Talos API surface the reconciler needs for one
// machine (architecture.md's "Configuration reconcile" steps 2 and 4). It
// is the one interface rule 0 allows: a real implementation dials machined
// over gRPC; tests use a fake, since no Talos call may ever reach a real
// node from a test (unit rule 2).
//
// Deliberately not on this interface: anything that decides reboot vs.
// no-reboot. ADR 0009 found that Talos's own ApplyConfiguration response
// cannot answer that (AUTO is a hardcoded alias for NO_REBOOT and never
// inspects the diff), so the reconciler decides from pkg/fieldmeta before
// it ever calls DryRunApply or ApplyNoReboot, and this interface has no
// method that could tempt it to ask Talos instead.
type TalosClient interface {
	// CurrentConfig returns the node's current machine configuration as
	// canonical YAML bytes (design step 2: "read the node's current
	// configuration over the Talos API"), the same encoding pkg/render
	// produces so the two hash comparably.
	CurrentConfig(ctx context.Context) ([]byte, error)

	// DryRunApply validates cfg against the node without applying it and
	// returns ValidateAtRuntime's warnings: deprecated-field migration
	// hints, missing cross-document configuration, label mismatches (ADR
	// 0009). It is always called before an apply, and is genuinely
	// side-effect free.
	DryRunApply(ctx context.Context, cfg []byte) (warnings []string, err error)

	// ApplyNoReboot applies cfg with ApplyConfigurationRequest_NO_REBOOT.
	// The reconciler only ever calls this once pkg/fieldmeta has said the
	// change needs neither a reboot nor a reinstall.
	ApplyNoReboot(ctx context.Context, cfg []byte) error

	// EtcdMemberList lists etcd members as seen by the node this client is
	// dialed to (removal.go: queried on the departing machine's own client
	// first, and on a surviving sibling's client as a fallback, to decide
	// whether the departing machine is still a member at all -- the check
	// half of removal.go's idempotent check-then-act, since
	// architecture.md records that "removing an already-removed etcd
	// member reports not-found, which counts as success").
	EtcdMemberList(ctx context.Context) ([]EtcdMember, error)

	// EtcdLeaveCluster asks the node this client is dialed to to leave the
	// etcd cluster gracefully. Call it on the departing machine's own
	// client: Talos's own `talosctl etcd leave` is explicit that this is
	// always preferred over EtcdRemoveMemberByID.
	EtcdLeaveCluster(ctx context.Context) error

	// EtcdRemoveMemberByID forcibly removes memberID from etcd, as decided
	// by the node this client is dialed to. Call it on a surviving
	// sibling's client when the departing machine cannot be dialed at all
	// (ADR 0032; architecture.md's forceRemove case: the node is dead or
	// unreachable, so it can never call EtcdLeaveCluster on itself).
	EtcdRemoveMemberByID(ctx context.Context, memberID uint64) error

	// Reset wipes this client's node back to maintenance mode: its STATE
	// and EPHEMERAL partitions only, never the whole system disk (ADR
	// 0038 -- Talos's own ResetRequest wipes everything, boot and EFI
	// included, unless told which partitions to name) and never
	// graceful (removal.go has already drained and left etcd itself by
	// the time this is ever called, so there is nothing left for Talos's
	// own graceful pre-reset routine to do). Opt-in per removal, and
	// fire-and-forget on Talos's side: this returns once the request is
	// accepted, not once the wipe finishes.
	Reset(ctx context.Context) error

	// Version reports this node's current Talos release tag (e.g.
	// "v1.14.3"), the "from" half of a Talos upgrade's pre-checks
	// (pkg/upgrade.TalosPath) and the signal talos_upgrade.go polls to tell
	// "upgrade not started yet" from "upgraded" from "rebooted but fell
	// back to its previous A/B slot" (architecture.md, "Upgrades").
	Version(ctx context.Context) (string, error)

	// BootID reads this node's current boot ID
	// (/proc/sys/kernel/random/boot_id), the same mechanism Talos's own
	// talosctl uses to confirm a reboot actually happened
	// (cmd/talosctl/pkg/talos/action/tracker.go's BootIDChangedPostCheckFn
	// and getBootID, Talos 1.14.3) -- Version alone cannot tell "hasn't
	// rebooted yet" apart from "rebooted and fell back", since both report
	// the same, unchanged version.
	BootID(ctx context.Context) (string, error)

	// Upgrade calls this node's Upgrade API with image (an installer image
	// reference), the default reboot mode, and preserve=false and
	// stage=false -- UpgradeRequest.Preserve is deprecated and the
	// installer ignores it on upgrade anyway (architecture.md's "Upgrades"
	// section, confirmed against Talos 1.14.3's source), so there is
	// nothing for a flag here to do. For a control-plane node this is also
	// where Talos validates etcd health before proceeding (its own
	// server-side ValidateForUpgrade, gated on force/preserve both being
	// false): Ichor does not re-implement that check, the same reasoning
	// architecture.md already gives for not re-implementing cordon/drain.
	Upgrade(ctx context.Context, image string) error
}

// EtcdMember is the one piece of each etcd member EtcdMemberList's callers
// need: enough to find the member matching a TalosMachine's
// Spec.Hostname (etcd reports the Node's own hostname, not an Ichor
// identity) and the numeric ID EtcdRemoveMemberByID takes.
type EtcdMember struct {
	ID       uint64
	Hostname string
}
