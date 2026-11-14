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

import (
	"context"
	"errors"
	"io"
)

// Errors of the etcd recovery calls that a retry or a different decision follows from.
var (
	// ErrRecoveryNotReady: the node is up but not ready to take the snapshot or the bootstrap yet (the
	// etcd service is not waiting for one, time is not in sync, cri is not healthy). Try again.
	ErrRecoveryNotReady = errors.New("node is not ready for etcd recovery yet")

	// ErrEtcdDataExists: the node's etcd data directory is not empty, so it cannot be bootstrapped.
	ErrEtcdDataExists = errors.New("etcd data directory is not empty")
)

// TalosClient is the Talos API surface the reconciler needs for one machine (architecture.md's
// "Configuration reconcile" steps 2 and 4): a real implementation dials machined over gRPC, tests use a
// fake, since no Talos call may reach a real node from a test (unit rule 2).
//
// Deliberately not on this interface: anything that decides reboot vs. no-reboot. Talos's own
// ApplyConfiguration response cannot answer that (AUTO is a hardcoded alias for NO_REBOOT), so the
// reconciler decides from pkg/fieldmeta before it calls DryRunApply or ApplyNoReboot.
type TalosClient interface {
	// CurrentConfig returns the node's current machine configuration as canonical YAML bytes, the encoding
	// pkg/render produces, so the two hash comparably.
	CurrentConfig(ctx context.Context) ([]byte, error)

	// DryRunApply validates cfg against the node without applying it and returns ValidateAtRuntime's
	// warnings (deprecated-field hints, missing cross-document configuration, label mismatches).
	// It is side-effect free and always called before an apply.
	DryRunApply(ctx context.Context, cfg []byte) (warnings []string, err error)

	// ApplyNoReboot applies cfg with ApplyConfigurationRequest_NO_REBOOT, once pkg/fieldmeta has said the
	// change needs neither a reboot nor a reinstall.
	ApplyNoReboot(ctx context.Context, cfg []byte) error

	// ApplyStaged applies cfg with ApplyConfigurationRequest_STAGED: it is written to disk and
	// takes effect on the next boot, nothing restarts. Only the config-rollout executor calls
	// it, and only just before Reboot; the mode is chosen by the
	// caller and never by Talos.
	ApplyStaged(ctx context.Context, cfg []byte) error

	// Reboot asks this node to reboot gracefully (not forced, so services stop in order). It does
	// NOT cordon or drain: Talos 1.14.3's Sequencer.Reboot has no drain phase (unlike Upgrade and
	// Shutdown with force=false), so the config rollout cordons and drains the node itself first
	// (drain.go). The RPC returns once the request is accepted.
	Reboot(ctx context.Context) error

	// EtcdMemberList lists etcd members as seen by the node this client is dialed to (removal.go: the
	// check half of its idempotent check-then-act, asked of the departing machine, else of a survivor).
	EtcdMemberList(ctx context.Context) ([]EtcdMember, error)

	// EtcdLeaveCluster asks the node this client is dialed to to leave etcd gracefully. Call it on the
	// departing machine's own client: `talosctl etcd leave` is always preferred over EtcdRemoveMemberByID.
	EtcdLeaveCluster(ctx context.Context) error

	// EtcdRemoveMemberByID forcibly removes memberID from etcd, as decided by the node this client is dialed
	// to. Call it on a survivor's client when the departing machine cannot be dialed.
	EtcdRemoveMemberByID(ctx context.Context, memberID uint64) error

	// Reset wipes this client's node back to maintenance mode: STATE and EPHEMERAL only, not the system disk
	// (Talos's own ResetRequest wipes boot and EFI too unless told which partitions), and not
	// gracefully (removal.go has drained and left etcd already). Fire-and-forget: it returns once the
	// request is accepted. wipeMode "system-disk" or "all" (TalosOperation.spec.node.wipeMode) wipes the
	// whole system disk so the node boots a boot image again, and "system-disk-and-user-disks" also every
	// user disk; empty keeps the STATE and EPHEMERAL wipe.
	// powerOff makes Talos power the node off after the wipe instead of rebooting it into maintenance mode.
	Reset(ctx context.Context, wipeMode string, powerOff bool) error

	// Shutdown powers this node off. force=false lets Talos cordon and
	// drain the node first (Sequencer.Shutdown, Talos 1.14.3: the drain
	// phase runs only when !force) and needs the Kubernetes API; force=true
	// skips that. Shutdown does not leave etcd (LeaveEtcd runs only in the
	// Reset sequence), so a stopped control plane stays an etcd member. The
	// RPC returns once the request is accepted, not once the node is down.
	Shutdown(ctx context.Context, force bool) error

	// Version reports this node's current Talos release tag (e.g. "v1.14.3"): the "from" of a Talos upgrade's
	// pre-checks (pkg/upgrade.TalosPath) and what talos_upgrade.go polls to tell "not started" from
	// "upgraded" from "rebooted but fell back to its previous A/B slot" (architecture.md, "Upgrades").
	Version(ctx context.Context) (string, error)

	// BootID reads this node's boot ID (/proc/sys/kernel/random/boot_id), as talosctl does to confirm a
	// reboot happened: Version alone cannot tell "not rebooted yet" from "rebooted and fell back".
	BootID(ctx context.Context) (string, error)

	// Upgrade calls this node's Upgrade API with image (an installer image reference), the default reboot
	// mode, preserve=false and stage=false (Preserve is deprecated and ignored by the installer on upgrade).
	// For a control plane Talos validates etcd health itself (ValidateForUpgrade); Ichor does not
	// re-implement that check.
	Upgrade(ctx context.Context, image string) error

	// PullImage pre-pulls image on this node, into the system containerd namespace when system is true (the
	// kubelet's image) and the CRI namespace otherwise (the static pods' images), as talosctl upgrade-k8s
	// does. A node that does not implement the RPC is not an error.
	PullImage(ctx context.Context, image string, system bool) error

	// Kubeconfig returns an admin kubeconfig issued by the control plane this client is dialed to, how the
	// Kubernetes upgrade writes the bootstrap manifests without broad Kubernetes RBAC for the controller.
	Kubeconfig(ctx context.Context) ([]byte, error)

	// BootstrapManifests returns the objects of the node's bootstrap manifest set (CoreDNS, kube-proxy,
	// inlineManifests, ...), as Talos renders them from the node's current machine configuration.
	BootstrapManifests(ctx context.Context) ([]map[string]any, error)

	// NodeName reports the Kubernetes node name this node registers under (its Nodename
	// resource), which Talos derives from the hostname. It is how the executors find the Node
	// and etcd member of a machine whose spec.hostname, an optional override, is unset.
	NodeName(ctx context.Context) (string, error)

	// ServiceRestart restarts one Talos service on this node. Callers pass only ids from
	// nodeActionServices; the interface does not enforce the allowlist.
	ServiceRestart(ctx context.Context, id string) error

	// ServiceState returns one Talos service's state ("Running", ...) and, when the service has a
	// health check, whether it is healthy; hasHealth is false when it has none.
	ServiceState(ctx context.Context, id string) (state string, hasHealth, healthy bool, err error)

	// EtcdStatus reports this node's own etcd member: its ID and the ID of the member it
	// believes leads, so the caller can tell whether it is the leader.
	EtcdStatus(ctx context.Context) (EtcdStatus, error)

	// EtcdAlarms lists the etcd alarms set cluster-wide as this node's member sees them
	// ("NOSPACE", "CORRUPT"); empty when there are none.
	EtcdAlarms(ctx context.Context) ([]string, error)

	// EtcdPKI reports which CA certificates this node's etcd trusts and which of them signed its etcd
	// server certificate, as the node's configuration has Talos set them up. It is what a
	// CA rotation checks before it restarts the member to load them.
	EtcdPKI(ctx context.Context) (EtcdPKI, error)

	// EtcdDefragment defragments this node's own etcd member. It blocks until the member is done.
	EtcdDefragment(ctx context.Context) error

	// EtcdForfeitLeadership asks this node's etcd member, if it leads, to hand leadership to
	// another member.
	EtcdForfeitLeadership(ctx context.Context) error

	// EtcdDataPresent reports whether this node has an etcd data directory (/var/lib/etcd/member).
	// It is how an etcd restore tells a control plane that is wiped from one that is not,
	// and a recovered one from both, without any state of its own.
	EtcdDataPresent(ctx context.Context) (bool, error)

	// ResetEtcdData wipes this node's etcd data the way Talos's disaster-recovery procedure does: a
	// non-graceful reset with reboot of the EPHEMERAL partition and of every dedicated-partition system
	// volume (never STATE: the machine configuration and identity stay). Fire-and-forget on Talos's
	// side. The node does not leave etcd first and does not drain.
	ResetEtcdData(ctx context.Context) error

	// EtcdRecover uploads an etcd snapshot to this node (for the next bootstrap with recovery). A
	// node that is not yet ready for it returns an error wrapping ErrRecoveryNotReady.
	EtcdRecover(ctx context.Context, snapshot io.Reader) error

	// BootstrapRecover bootstraps etcd on this node from the uploaded snapshot (Bootstrap with
	// RecoverEtcd). It wraps ErrRecoveryNotReady when the node is not ready yet and ErrEtcdDataExists
	// when its data directory is not empty.
	BootstrapRecover(ctx context.Context) error

	// MachineStage reads the node's MachineStatus stage ("running",
	// "installing", "rebooting", ...), which followStage uses to follow a
	// node Ichor provisioned until it is running.
	MachineStage(ctx context.Context) (string, error)

	// Schematic reports the Image Factory schematic ID this node's running system was built from
	// (the ImageFactorySchematic resource, which Talos fills from the "schematic" extension that
	// Image Factory bakes into every asset and installer image). Empty with a nil error when the
	// node did not boot an Image Factory asset.: it is how a schematic-only
	// change is told apart from "already up to date".
	Schematic(ctx context.Context) (string, error)

	// SecurityState reports the node's boot security: SecureBoot, UKI, signed modules and the
	// kernel command line (the SecurityState and KernelCmdline resources).
	SecurityState(ctx context.Context) (SecurityState, error)

	// APIServerInstance identifies this control plane's running kube-apiserver container (the
	// container ID in the kube-apiserver static pod's status, from Talos's StaticPodStatus
	// resource). It changes when the pod is recreated, which is how the config rollout tells "the
	// restart has not started yet" from "restarted". Empty with a nil error when
	// the pod reports no container.
	APIServerInstance(ctx context.Context) (string, error)

	// APIServerReady asks this control plane's own kube-apiserver for /readyz, through the node's
	// own address (the one the Talos API is dialed on), and returns nil only for HTTP 200.
	APIServerReady(ctx context.Context) error

	// Identity reads this node's hardware identity (SMBIOS UUID, serial, manufacturer and the
	// permanent MAC of each physical link) over the authenticated API: the same resources as
	// MaintenanceClient.Identity, for a node that already has a configuration.
	Identity(ctx context.Context) (HardwareIdentity, error)
}

// EtcdMember is what EtcdMemberList's callers need of a member: its hostname (the Node's own, matched
// against TalosMachine.spec.hostname) and the ID EtcdRemoveMemberByID takes.
type EtcdMember struct {
	ID       uint64
	Hostname string
}

// EtcdStatus is the part of a member's status a node action decides on.
type EtcdStatus struct {
	// MemberID is the queried member's own ID; LeaderID the ID of the member it sees as leader.
	MemberID, LeaderID uint64
	// DBSize and DBSizeInUse are bytes.
	DBSize, DBSizeInUse int64
}

// IsLeader reports whether the queried member leads.
func (s EtcdStatus) IsLeader() bool { return s.MemberID != 0 && s.MemberID == s.LeaderID }
