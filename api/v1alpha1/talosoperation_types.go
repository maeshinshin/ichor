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

package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// Values for TalosOperationSpec.Kind. Each names which later, separate unit
// claims this operation's actual execution (docs/design/architecture.md,
// "Upgrades"); this CRD and its controller only store and report the value
// -- neither ever branches on it.
const (
	TalosOperationKindTalos      = "talos"
	TalosOperationKindKubernetes = "kubernetes"
	TalosOperationKindIchor      = "ichor"
	TalosOperationKindCNI        = "cni"

	// TalosOperationKindShutdown stops every machine of the cluster in an
	// order that keeps the Kubernetes API up as long as possible.
	// Unlike the upgrade kinds it is not a rollout: TalosOperationReconciler
	// dispatches it to its own state machine, and while it has not
	// Completed every other disruptive operation on the cluster is refused.
	TalosOperationKindShutdown = "shutdown"

	// TalosOperationKindCARotation replaces the Talos API CA and the Kubernetes API CA, and with
	// spec.caRotation the etcd CA and the aggregator CA, in the three
	// passes (accept the new CA, make it the issuer, drop the old one). Like shutdown it
	// runs its own state machine, held in status.caRotation, instead of the per-machine rollout.
	TalosOperationKindCARotation = "ca-rotation"

	// TalosOperationKindConfig applies the pending reboot-requiring configuration changes of the
	// approved machines, one machine at a time through the node-operation hook, a staged apply and
	// a reboot. Nothing reboots a node for a configuration change without one.
	TalosOperationKindConfig = "config"

	// TalosOperationKindNode runs one action on exactly one machine (spec.node): a
	// reboot, a shutdown, a service restart, an etcd defragmentation or leadership handover, or a
	// reset. Like shutdown it runs its own state machine, held in status.node.
	TalosOperationKindNode = "node"

	// TalosOperationKindEtcdRestore rewinds the whole cluster to a stored etcd snapshot (spec.etcdRestore): etcd is
	// wiped on every control plane and recovered on one from the snapshot. Like
	// shutdown it runs its own state machine, held in status.etcdRestore and, while the Kubernetes API
	// is away, in a marker next to the backups.
	TalosOperationKindEtcdRestore = "etcd-restore"
)

// Values for TalosOperationEtcdRestoreStatus.Step, in the order they run.
const (
	// EtcdRestoreStepWiping: the snapshot is verified and pinned, the marker is written, and etcd data is
	// being wiped on every control plane (their EPHEMERAL partitions are reset and the nodes reboot).
	EtcdRestoreStepWiping = "Wiping"

	// EtcdRestoreStepRecovering: every control plane is wiped; the snapshot is uploaded to the target and
	// its etcd is bootstrapped from it.
	EtcdRestoreStepRecovering = "Recovering"

	// EtcdRestoreStepRejoining: etcd on the target was recovered; the other control planes rejoin it and
	// the Kubernetes API comes back. From here the cluster state is the snapshot's.
	EtcdRestoreStepRejoining = "Rejoining"

	// EtcdRestoreStepReconciling: etcd has every member and the Kubernetes API answers; every TalosMachine
	// was asked to reconcile again and the operation waits until each is Ready.
	EtcdRestoreStepReconciling = "Reconciling"
)

// EtcdRestoreReconcileAnnotation, set by an etcd-restore operation on every TalosMachine, holds a token
// the machine controller copies to status.observedReconcileRequest once it has reconciled the machine
// after the request. It is how the restore knows each machine looked at the restored state.
const EtcdRestoreReconcileAnnotation = "ichor.maesh.dev/reconcile-requested"

// OperationRetryAnnotation, set to OperationRetryRequested (`kubectl annotate talosoperation NAME
// ichor.maesh.dev/retry=true`, or `ichor resume operation`, or the UI) on an operation paused by a failed
// step, asks the controller to run the failed step again. The controller never changes spec, so the
// request is an annotation; it acts once and removes it, so the next "true" is a new request.
// Any other value is ignored.
const OperationRetryAnnotation = "ichor.maesh.dev/retry"

// OperationRetryRequested is the one value of OperationRetryAnnotation the controller acts on.
const OperationRetryRequested = "true"

// OperationStartedByAnnotation records who created the operation through the UI backend (the signed-in
// user, or the backend's own name). Informational only: the audit trail is the Events and the Kubernetes
// audit log.
const OperationStartedByAnnotation = "ichor.maesh.dev/started-by"

// OperationFromVersionAnnotation records the version a Talos or Kubernetes upgrade moves from, stamped
// by the UI backend when it creates the operation together with the spec change. Display only.
const OperationFromVersionAnnotation = "ichor.maesh.dev/from-version"

// Values for TalosOperationNodeSpec.Action.
const (
	NodeActionReboot                = "reboot"
	NodeActionShutdown              = "shutdown"
	NodeActionRestartService        = "restart-service"
	NodeActionEtcdDefrag            = "etcd-defrag"
	NodeActionEtcdForfeitLeadership = "etcd-forfeit-leadership"
	NodeActionReset                 = "reset"
)

// Values for TalosOperationNodeSpec.WipeMode.
const (
	// WipeModeSystemDisk wipes the whole system disk, boot and EFI included.
	WipeModeSystemDisk = "system-disk"
	// WipeModeAll wipes the whole system disk, boot and EFI included, the same as WipeModeSystemDisk. It does not wipe user disks.
	WipeModeAll = "all"
	// WipeModeSystemDiskAndUserDisks wipes the whole system disk and every user disk: each block device
	// except the system disk, CD-ROM drives and read-only disks, which are left out.
	WipeModeSystemDiskAndUserDisks = "system-disk-and-user-disks"
)

// Values for TalosOperationNodeStatus.Step.
const (
	// NodeStepRequested: the action was sent to the node; the operation waits for its effect.
	NodeStepRequested = "Requested"

	// NodeStepRemoving: a reset handed the machine to the removal flow and waits for it to go.
	NodeStepRemoving = "Removing"
)

// Values for TalosOperationKubernetesStatus.Stage, in the order they run.
const (
	// KubernetesStageAPIServers: kube-apiserver moves to the target on every control plane, one at
	// a time; controller-manager, scheduler, kubelets and kube-proxy stay. The v1.37 scheduler needs
	// APIs only a v1.37 apiserver serves, and it reaches the apiservers through an endpoint that
	// balances across all of them, so no controller-manager or scheduler may move before every
	// apiserver has.
	KubernetesStageAPIServers = "APIServers"

	// KubernetesStageControlPlaneComponents: controller-manager and scheduler move to the target on
	// every control plane, one control plane at a time; kubelets and kube-proxy stay.
	KubernetesStageControlPlaneComponents = "ControlPlaneComponents"

	// KubernetesStageKubeProxy: kube-proxy moves to the target and the bootstrap manifests are synced.
	KubernetesStageKubeProxy = "KubeProxy"

	// KubernetesStageKubelets: every kubelet moves to the target, control planes first.
	KubernetesStageKubelets = "Kubelets"
)

// Values for TalosOperationCARotationStatus.Stage, in the order they run. The stage names which CA
// every node is being given: it is the whole answer to "which CA is accepted and which issues".
const (
	// CARotationStageAccepting: the new CAs are added to every node's accepted CAs; the old CAs
	// still issue. The new CAs' keys are already in the bundle Secret's rotation.yaml.
	CARotationStageAccepting = "Accepting"

	// CARotationStageAwaitingCredentials: Accepting is verified on every node (both CAs accepted, the
	// old ones still issue) and the operation waits, changing nothing, until the operator has new
	// credentials: the CLI writes them and sets CredentialsHandoffAnnotation. Nothing the
	// operator holds stops working before this stage ends.
	CARotationStageAwaitingCredentials = "AwaitingCredentials"

	// CARotationStageIssuing: the new CAs issue on every node; the old CAs are still accepted.
	CARotationStageIssuing = "Issuing"

	// CARotationStageDropping: the old CAs are removed from every node's accepted CAs.
	CARotationStageDropping = "Dropping"

	// CARotationStageFinalizing: every node runs on the new CAs alone. The bundle Secret's
	// secrets.yaml is replaced by the new CAs and rotation.yaml removed, then Ichor's own dial is
	// checked against the replaced bundle.
	CARotationStageFinalizing = "Finalizing"

	// CARotationStageDone: the rotation is complete.
	CARotationStageDone = "Done"
)

// ConditionCAClientsNotRestarted is the TalosCluster condition that is True (reason CARotated) when a
// CA rotation completed but in-cluster clients that keep the old CA were not available again within
// the bound (CNI-first amendment). The next rotation that restarts them cleanly sets it False.
const ConditionCAClientsNotRestarted = "CAClientsNotRestarted"

// ConditionBundleBackupStale is the TalosCluster condition that is True from the end of a CA
// rotation until the user has exported the new secrets bundle.
const ConditionBundleBackupStale = "BundleBackupStale"

// CredentialsHandoffAnnotation, set on a ca-rotation TalosOperation by `ichor rotate ca` (or
// `ichor create credentials --acknowledge`) after it has written the operator a kubeconfig and a
// talosconfig that work with both the old and the new CAs, holds CARotationHandoffToken of the
// operation's status. The controller leaves the AwaitingCredentials stage only when it matches.
// It checks nothing else: setting the annotation by hand is an attestation that you hold a working
// kubeconfig and talosconfig for the new CAs. `ichor create credentials --acknowledge` and the
// UI verify the new kubeconfig against the cluster before they set it.
const CredentialsHandoffAnnotation = "ichor.maesh.dev/credentials-handoff"

// CARotationHandoffToken is what CredentialsHandoffAnnotation must hold: the fingerprints of the new
// CAs, so an acknowledgement given for another rotation does not release this one. Empty before the
// new CAs are known.
func CARotationHandoffToken(rs *TalosOperationCARotationStatus) string {
	if rs == nil || len(rs.Certificates) == 0 {
		return ""
	}

	parts := make([]string, 0, len(rs.Certificates))
	for _, c := range rs.Certificates {
		parts = append(parts, c.Name+"="+c.Next)
	}

	return strings.Join(parts, ",")
}

// BundleExportedAnnotation, set on a TalosCluster by `ichor export bundle` after it has saved
// the bundle, holds the fingerprint (secretsbundle.Fingerprint) of the bundle it saved. When it
// equals the live bundle's fingerprint the controller clears BundleBackupStale.
const BundleExportedAnnotation = "ichor.maesh.dev/bundle-exported"

// Values for TalosOperationStatus.Phase.
const (
	// TalosOperationPhasePending is the default: created, nothing advanced yet.
	TalosOperationPhasePending = "Pending"

	// TalosOperationPhaseInProgress means the lock is held and at least one
	// machine has been advanced.
	TalosOperationPhaseInProgress = "InProgress"

	// TalosOperationPhasePaused means spec.paused is true, or a machine's
	// step failed (pause-on-failure): no further machine is advanced, but
	// nothing already completed is undone.
	TalosOperationPhasePaused = "Paused"

	// TalosOperationPhaseShutDown is the shutdown kind's resting phase:
	// every machine has been asked to stop and the operation is waiting for
	// the operator to power them back on. The cluster is still marked as
	// shutting down; the operation becomes Completed, and the mark goes
	// away, once every machine answers again on a new boot.
	TalosOperationPhaseShutDown = "ShutDown"

	// TalosOperationPhaseCompleted means every eligible machine finished;
	// the cluster lock has been released.
	TalosOperationPhaseCompleted = "Completed"
)

// Values for TalosOperationMachineStatus.Phase.
const (
	TalosOperationMachinePhaseInProgress = "InProgress"
	TalosOperationMachinePhaseCompleted  = "Completed"
)

// TalosMachineUpgradeHoldAnnotation, set on a TalosMachine, excludes it from
// every TalosOperation's selection -- the design's "per-machine hold
// annotation" (docs/design/architecture.md, "Upgrades"). Any non-empty
// value holds; there is no "hold until" value to parse.
const TalosMachineUpgradeHoldAnnotation = "ichor.maesh.dev/upgrade-hold"

// TalosMachineUpgradeOrderLabel, set on a TalosMachine, orders it within its
// tier (control planes among themselves, workers among themselves) when a
// TalosOperation selects which machines to advance next -- the design's
// "label-based ordering" (docs/design/architecture.md, "Upgrades"). Compared
// as a plain string (Kubernetes label values have no numeric type); a
// machine without the label sorts after every machine that has one.
const TalosMachineUpgradeOrderLabel = "ichor.maesh.dev/upgrade-order"

// TalosOperationSpec defines the desired state of TalosOperation: the
// backbone every upgrade kind shares (docs/design/architecture.md,
// "Upgrades") -- which cluster, which kind, and the pause/concurrency
// controls -- never that kind's own execution details.
// +kubebuilder:validation:XValidation:rule="self.clusterRef == oldSelf.clusterRef",message="clusterRef cannot change after creation"
// +kubebuilder:validation:XValidation:rule="self.kind == oldSelf.kind",message="kind cannot change after creation"
// +kubebuilder:validation:XValidation:rule="(self.kind == 'node') == has(self.node)",message="spec.node is required for the node kind and only allowed there"
// +kubebuilder:validation:XValidation:rule="!has(self.cni) || self.kind == 'cni'",message="spec.cni is only allowed for the cni kind"
// +kubebuilder:validation:XValidation:rule="has(self.cni) == has(oldSelf.cni) && (!has(self.cni) || self.cni == oldSelf.cni)",message="cni cannot change after creation"
// +kubebuilder:validation:XValidation:rule="(self.kind == 'key-rotation') == has(self.keyRotation)",message="spec.keyRotation is required for the key-rotation kind and only allowed there"
// +kubebuilder:validation:XValidation:rule="has(self.keyRotation) == has(oldSelf.keyRotation) && (!has(self.keyRotation) || self.keyRotation == oldSelf.keyRotation)",message="keyRotation cannot change after creation"
// +kubebuilder:validation:XValidation:rule="(self.kind == 'etcd-restore') == has(self.etcdRestore)",message="spec.etcdRestore is required for the etcd-restore kind and only allowed there"
// +kubebuilder:validation:XValidation:rule="!has(self.etcdRestore) || self.etcdRestore.confirm == self.clusterRef.name",message="an etcd restore needs spec.etcdRestore.confirm equal to the cluster name"
// +kubebuilder:validation:XValidation:rule="has(self.etcdRestore) == has(oldSelf.etcdRestore) && (!has(self.etcdRestore) || self.etcdRestore == oldSelf.etcdRestore)",message="etcdRestore cannot change after creation"
// +kubebuilder:validation:XValidation:rule="has(self.node) == has(oldSelf.node) && (!has(self.node) || self.node == oldSelf.node)",message="node cannot change after creation"
// +kubebuilder:validation:XValidation:rule="!has(self.caRotation) || self.kind == 'ca-rotation'",message="spec.caRotation is only allowed for the ca-rotation kind"
// +kubebuilder:validation:XValidation:rule="has(self.caRotation) == has(oldSelf.caRotation) && (!has(self.caRotation) || self.caRotation == oldSelf.caRotation)",message="caRotation cannot change after creation"
type TalosOperationSpec struct {
	// clusterRef names the TalosCluster this operation acts on.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// kind names which upgrade this is: talos (spec.talosVersion/schematic),
	// kubernetes (spec.kubernetesVersion), or ichor (the controller/CRDs/UI
	// install), cni (a spec.cni.kubeProxy switch), shutdown (a
	// planned stop of the whole cluster), or ca-rotation (the Talos API and
	// Kubernetes API CAs). This CRD and its controller only
	// store and report it -- each kind's own execution is a separate unit's work.
	// shutdown and ca-rotation are the exceptions: each runs its own state machine.
	// config applies the pending reboot-requiring configuration changes, one machine at a time
	// with a reboot each.
	// etcd-restore rewinds the cluster to a stored etcd snapshot.
	// key-rotation replaces the service-account signing key and the secretbox key.
	// +kubebuilder:validation:Enum=talos;kubernetes;ichor;cni;shutdown;ca-rotation;config;node;etcd-restore;key-rotation
	// +required
	Kind string `json:"kind"`

	// keyRotation is the key-rotation kind's request: which keys, and how long the old service-account key is kept.
	// +optional
	KeyRotation *TalosOperationKeyRotationSpec `json:"keyRotation,omitempty"`

	// etcdRestore is the snapshot of an etcd-restore-kind operation.
	// +optional
	EtcdRestore *TalosOperationEtcdRestoreSpec `json:"etcdRestore,omitempty"`

	// caRotation chooses which CAs a ca-rotation-kind operation replaces. Absent means the
	// Talos API CA and the Kubernetes API CA, as before.
	// +optional
	CARotation *TalosOperationCARotationSpec `json:"caRotation,omitempty"`

	// cni says which cni-kind operation this is. Absent means the kube-proxy switch;
	// action upgrade is a Cilium chart upgrade.
	// +optional
	CNI *TalosOperationCNISpec `json:"cni,omitempty"`

	// node is the action of a node-kind operation: which machine and what to do to it.
	// +optional
	Node *TalosOperationNodeSpec `json:"node,omitempty"`

	// paused stops the operation from advancing any further machine. It
	// does not undo a machine already advanced, and it does not release
	// the cluster lock -- the operation is not finished, only halted.
	// It has no effect on an etcd restore that has started: the
	// restore takes the Kubernetes API away and the controller drives it to the
	// end, or to a failed step that pauses it, whatever this field says.
	// +kubebuilder:default=false
	// +optional
	Paused bool `json:"paused,omitempty"`

	// maxUnavailable caps how many worker machines this operation advances
	// at once. Control planes are never parallel: this operation advances
	// exactly one at a time, and only once every control plane has
	// finished, regardless of this value.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	MaxUnavailable int32 `json:"maxUnavailable,omitempty"`
}

// Names of the CAs a ca-rotation can replace (spec.caRotation.targets). They are the names the cluster's
// status.certificates uses for the same CAs.
const (
	CARotationTargetTalos      = "talos"
	CARotationTargetKubernetes = "kubernetes"
	CARotationTargetEtcd       = "etcd"
	CARotationTargetAggregator = "kubernetes-aggregator"
)

// DefaultCARotationTargets are the CAs a rotation replaces when spec.caRotation is absent.
func DefaultCARotationTargets() []string {
	return []string{CARotationTargetTalos, CARotationTargetKubernetes}
}

// TalosOperationCARotationSpec is the choice of CAs of a ca-rotation operation.
type TalosOperationCARotationSpec struct {
	// targets lists the CAs to replace: talos, kubernetes, etcd, kubernetes-aggregator. Empty means
	// talos and kubernetes. The set is fixed when the rotation starts; the new CAs saved in the bundle
	// Secret are what a restarted controller follows.
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:items:Enum=talos;kubernetes;etcd;kubernetes-aggregator
	// +listType=set
	// +optional
	Targets []string `json:"targets,omitempty"`
}

// TalosOperationCNIAction values.
const TalosOperationCNIActionUpgrade = "upgrade"

// TalosOperationCNISpec makes a cni-kind operation something other than a kube-proxy switch.
type TalosOperationCNISpec struct {
	// action is upgrade: run the Cilium Helm Job to move the release to the chart this Ichor embeds.
	// +kubebuilder:validation:Enum=upgrade
	// +required
	Action string `json:"action"`

	// chartVersion is the Cilium chart version the operation upgrades to. The executor refuses to
	// run when the controller embeds another version (it was upgraded after the operation was created).
	// +kubebuilder:validation:MinLength=1
	// +required
	ChartVersion string `json:"chartVersion"`

	// imageVersion is deprecated and unused. An earlier Ichor made the upgrade a Cilium patch with it. Cilium now moves
	// only to the chart the running Ichor embeds (amendment 2026-11-04
	// (3)): the executor refuses an operation that sets it. Kept so the schema does not lose a field an
	// installed build had.
	// +optional
	// +kubebuilder:validation:Pattern=`^v[0-9]+\.[0-9]+\.[0-9]+$`
	ImageVersion string `json:"imageVersion,omitempty"`
}

// TalosOperationEtcdRestoreSpec names the snapshot an etcd-restore operation puts back.
type TalosOperationEtcdRestoreSpec struct {
	// backupRef names the TalosEtcdBackup (same namespace) whose storage holds the snapshot. Its
	// credentials, prefix and the cluster's secrets bundle (the decryption key) are what the
	// controller reads the snapshot with.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="backupRef.name is required"
	// +required
	BackupRef corev1.LocalObjectReference `json:"backupRef"`

	// snapshot is the object name of the snapshot under the backup's prefix, as listed in the
	// backup's status.snapshots.
	// +kubebuilder:validation:Pattern=`^etcd-[0-9]{8}T[0-9]{6}Z\.snapshot\.age$`
	// +required
	Snapshot string `json:"snapshot"`

	// confirm must equal the cluster's name: the restore rewinds the whole cluster, so the name is typed.
	// +required
	Confirm string `json:"confirm"`

	// reason is deprecated: unused, kept so the schema never drops a field. It was the
	// override reason the restore was started under; it is no longer required, asked for or read.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// TalosOperationNodeSpec is one action on one machine. One action per operation.
// +kubebuilder:validation:XValidation:rule="self.action == 'restart-service' ? has(self.service) : !has(self.service)",message="service is required for restart-service and only allowed there"
// +kubebuilder:validation:XValidation:rule="self.action != 'reset' || (has(self.confirm) && self.confirm == self.machineRef.name)",message="reset needs confirm equal to the machine name"
// +kubebuilder:validation:XValidation:rule="!self.allowLeader || self.action == 'etcd-defrag'",message="allowLeader only applies to etcd-defrag"
// +kubebuilder:validation:XValidation:rule="!has(self.wipeMode) || self.action == 'reset'",message="wipeMode only applies to reset"
// +kubebuilder:validation:XValidation:rule="!has(self.powerOff) || self.action == 'reset'",message="powerOff only applies to reset"
type TalosOperationNodeSpec struct {
	// machineRef names the TalosMachine the action runs on.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="machineRef.name is required"
	// +required
	MachineRef corev1.LocalObjectReference `json:"machineRef"`

	// action is what to do: reboot, shutdown, restart-service, etcd-defrag (control planes),
	// etcd-forfeit-leadership (the control plane that leads etcd), or reset (the removal flow with
	// the node wiped). etcd member removal is not an action: it is part of removal.
	// +kubebuilder:validation:Enum=reboot;shutdown;restart-service;etcd-defrag;etcd-forfeit-leadership;reset
	// +required
	Action string `json:"action"`

	// service is the Talos service restart-service restarts. Only kubelet, containerd and cri
	// are allowed; etcd, apid, trustd and machined never are.
	// +kubebuilder:validation:Enum=kubelet;containerd;cri
	// +optional
	Service string `json:"service,omitempty"`

	// allowLeader lets etcd-defrag run on the member that currently leads a multi-member etcd.
	// By default it is refused there.
	// +kubebuilder:default=false
	// +optional
	AllowLeader bool `json:"allowLeader,omitempty"`

	// confirm must equal the machine's name for reset, the typed confirmation of an irreversible action.
	// +optional
	Confirm string `json:"confirm,omitempty"`

	// override asks to override a safety check: the last-worker guard for reboot and shutdown, or the
	// reset's own gate. It is only honoured when the creator holds the
	// override verb on the machine (the override webhook checks), and it goes into the audit record and
	// an Event.
	// +optional
	Override bool `json:"override,omitempty"`

	// overrideReason is deprecated: unused, kept so the schema never drops a field. It was the
	// optional reason for an override; no client sets it and nothing reads it. A value written
	// by an older build still counts as an override (see Overrides).
	// +kubebuilder:validation:MinLength=1
	// +optional
	OverrideReason string `json:"overrideReason,omitempty"`

	// wipeMode makes a reset wipe more than the STATE and EPHEMERAL partitions: system-disk erases
	// the whole system disk (boot and EFI included, so the node boots a boot image again instead of
	// its installed UKI, which is what installing it afresh from a SecureBoot ISO needs), all erases the
	// same system disk and never a user disk; system-disk-and-user-disks erases that system disk and every user
	// disk too. Only with the reset action; unset keeps the STATE and EPHEMERAL wipe.
	// +kubebuilder:validation:Enum=system-disk;all;system-disk-and-user-disks
	// +optional
	WipeMode string `json:"wipeMode,omitempty"`

	// powerOff makes the node power off after the reset wipes it instead of rebooting into maintenance
	// mode. Only with the reset action; unset keeps the reboot, which is what a reset did before
	// the field existed. The UI and `ichor delete node` set it unless asked to reboot.
	// +optional
	PowerOff bool `json:"powerOff,omitempty"`

	// breakGlassReason is the earlier name of overrideReason, kept because CRD fields are append-only
	// and operations written by older builds carry it. Deprecated: use override.
	// Ichor's own CLI and UI never set it; its text is not read, but a value still counts, and is gated
	// by the same verb, as an override.
	// +kubebuilder:validation:MinLength=1
	// +optional
	BreakGlassReason string `json:"breakGlassReason,omitempty"`
}

// Overrides reports whether the spec asks to override a safety check: override is set, or a reason is
// given in overrideReason or the deprecated breakGlassReason. A reason alone still counts, so operations
// written before override was a flag keep their gate.
func (n *TalosOperationNodeSpec) Overrides() bool {
	return n.Override || n.OverrideReason != "" || n.BreakGlassReason != ""
}

// TalosOperationNodeStatus is the state of a node-kind operation. The machine's own entry in
// status.machines holds the boot ID read just before the action was sent.
type TalosOperationNodeStatus struct {
	// step is empty until the action was sent, then Requested (waiting for its effect) or
	// Removing (a reset waiting for the removal flow).
	// +kubebuilder:validation:Enum=Requested;Removing
	// +optional
	Step string `json:"step,omitempty"`

	// requestedAt is when the action was sent; the wait for its effect is bounded from it.
	// +optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`

	// message is a plain-English line about what the operation is doing or waiting for.
	// +optional
	Message string `json:"message,omitempty"`

	// machineUID is the UID of the TalosMachine a reset started on. The wait for the removal
	// flow ends when that object is gone or replaced by one with another UID: discovery may
	// recreate a pending machine with the same name within seconds, and that one is never the
	// operation's to delete.
	// +optional
	MachineUID types.UID `json:"machineUID,omitempty"`
}

// TalosOperationEtcdRestoreStatus is the visible state of an etcd restore. The state that survives the
// rollback of the Kubernetes API is the marker object next to the backups; this mirrors it
// whenever the API can be written, and the operation is recreated from the marker after the rollback.
type TalosOperationEtcdRestoreStatus struct {
	// step is Wiping, Recovering, Rejoining or Reconciling; empty until the snapshot was verified and
	// the marker written.
	// +kubebuilder:validation:Enum=Wiping;Recovering;Rejoining;Reconciling
	// +optional
	Step string `json:"step,omitempty"`

	// target is the control plane the snapshot is recovered on.
	// +optional
	Target string `json:"target,omitempty"`

	// snapshotTime is when the snapshot being restored was taken.
	// +optional
	SnapshotTime *metav1.Time `json:"snapshotTime,omitempty"`

	// requestedAt is when the destructive part started; waits are bounded from it.
	// +optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`

	// reconcileRequest is the token every TalosMachine was asked to acknowledge in the last step.
	// +optional
	ReconcileRequest string `json:"reconcileRequest,omitempty"`

	// message is a plain-English line about what the operation is doing or waiting for.
	// +optional
	Message string `json:"message,omitempty"`
}

// TalosOperationMachineStatus is one machine's progress within a
// TalosOperation.
type TalosOperationMachineStatus struct {
	// name is the TalosMachine's name.
	// +required
	Name string `json:"name"`

	// phase is this machine's own progress: InProgress (advance was called,
	// not yet done) or Completed (advance reported done). A machine never
	// appears here until it has been selected at least once; one that is
	// held, or simply not yet reached, is absent, not listed as some third
	// phase.
	// +kubebuilder:validation:Enum=InProgress;Completed
	// +required
	Phase string `json:"phase"`

	// bootID is the machine's boot ID recorded just before it was asked to
	// stop (shutdown kind only; empty for the other kinds, and for a machine
	// that was already unreachable). A different boot ID later is how the
	// operation knows the machine has been powered back on.
	// +optional
	BootID string `json:"bootID,omitempty"`

	// startedAt is when the operation first selected this machine.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// finishedAt is when this machine's step was reported done.
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`
}

// TalosOperationStatus defines the observed state of TalosOperation.
type TalosOperationStatus struct {
	// phase is this operation's own state-machine phase (see the
	// TalosOperationPhase* constants). The whole state machine lives here,
	// not in the controller's memory, so a restart resumes from exactly
	// this value and the machines list below, not from scratch
	// (docs/design/architecture.md, "Upgrades": "the status-held state
	// machine").
	// +kubebuilder:validation:Enum=Pending;InProgress;Paused;ShutDown;Completed
	// +optional
	Phase string `json:"phase,omitempty"`

	// machines records every machine this operation has ever selected, and
	// how far each got. A name's absence means "never selected" -- held,
	// not yet its turn, or the operation finished before it was reached --
	// not a fourth phase value.
	// +listType=map
	// +listMapKey=name
	// +optional
	Machines []TalosOperationMachineStatus `json:"machines,omitempty"`

	// failureReason explains why phase is Paused when the cause is a
	// machine's own failed step, as opposed to spec.paused.
	// +optional
	FailureReason string `json:"failureReason,omitempty"`

	// message is what an InProgress operation is doing or waiting for right now, as the controller
	// last saw it ("waiting for the ichor-ui Deployment to finish rolling out (1 of 2 updated
	// replicas are available)"). It is replaced when it changes and cleared in every other phase;
	// a pause says why in failureReason or the Paused condition, a finished operation in its Events.
	// +optional
	Message string `json:"message,omitempty"`

	// fromVersion is deprecated and unused: Ichor no longer writes or reads it. The from-version
	// now lives in the ichor.maesh.dev/from-version annotation (OperationFromVersionAnnotation).
	// The field stays so the CRD never drops a field an installed version has.
	// +optional
	FromVersion string `json:"fromVersion,omitempty"`

	// toVersion is the Ichor version an ichor operation upgrades to: the controller copies
	// IchorInstallation.spec.version here once, when the upgrade starts, and never rewrites it.
	// It is empty on other kinds and on operations that started before it existed.
	// +optional
	ToVersion string `json:"toVersion,omitempty"`

	// finishedAt is when the operation reached Completed.
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// lastRetry is the time of the retry request (the ichor.maesh.dev/retry annotation) the
	// controller last acted on: it cleared failureReason and ran the failed step again.
	// +optional
	LastRetry *metav1.Time `json:"lastRetry,omitempty"`

	// node is the node kind's state machine.
	// +optional
	Node *TalosOperationNodeStatus `json:"node,omitempty"`

	// etcdRestore is the etcd-restore kind's state machine.
	// +optional
	EtcdRestore *TalosOperationEtcdRestoreStatus `json:"etcdRestore,omitempty"`

	// kubernetes is the kubernetes kind's state machine: which of the three stages the machines
	// are being taken through.
	// +optional
	Kubernetes *TalosOperationKubernetesStatus `json:"kubernetes,omitempty"`

	// caRotation is the ca-rotation kind's state machine: which stage every node is being taken
	// to, and which CA is accepted and which issues in it. Public fingerprints only.
	// +optional
	CARotation *TalosOperationCARotationStatus `json:"caRotation,omitempty"`

	// keyRotation is the key-rotation kind's state machine: which stage every control plane is being taken to,
	// which key signs or encrypts in it, and the progress of the re-encryption. Public identifiers only.
	// +optional
	KeyRotation *TalosOperationKeyRotationStatus `json:"keyRotation,omitempty"`

	// conditions represent the current state of the TalosOperation resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TalosOperationKubernetesStatus is the stage a Kubernetes upgrade is in. status.machines
// holds the progress of that stage only; it is cleared when the stage changes.
type TalosOperationKubernetesStatus struct {
	// stage is APIServers (kube-apiserver on every control plane), ControlPlaneComponents
	// (controller-manager and scheduler on every control plane; the kubelets and kube-proxy still on
	// the old version), KubeProxy (kube-proxy and the bootstrap manifests) or Kubelets (every
	// kubelet, control planes first, then workers).
	// +kubebuilder:validation:Enum=APIServers;ControlPlaneComponents;KubeProxy;Kubelets
	// +optional
	Stage string `json:"stage,omitempty"`
}

// TalosOperationCARotationStatus is the whole state of a CA rotation. Nothing else holds it: a
// restarted controller continues from stage, and per-node progress inside a stage is re-derived
// from what each node reports.
type TalosOperationCARotationStatus struct {
	// stage is the stage every node is being taken to; see the CARotationStage* constants.
	// Empty until the new CAs have been generated and saved.
	// +kubebuilder:validation:Enum=Accepting;AwaitingCredentials;Issuing;Dropping;Finalizing;Done
	// +optional
	Stage string `json:"stage,omitempty"`

	// certificates lists the two rotated CAs and, for the current stage, which fingerprint issues
	// and which are accepted.
	// +listType=map
	// +listMapKey=name
	// +optional
	Certificates []CARotationCertificate `json:"certificates,omitempty"`

	// etcdRestarted names the control planes that were rebooted, and are healthy again, in the current
	// stage, in the order they were rebooted (Talos does not restart etcd on a configuration
	// change and its etcd service has no restart API, so a member loads the stage's CA with a node
	// reboot). Emptied when the stage changes.
	// +listType=set
	// +optional
	EtcdRestarted []string `json:"etcdRestarted,omitempty"`

	// etcdRestartedAt is when the reboot of the control plane in etcdRebooting, or the last one, was requested.
	// +optional
	EtcdRestartedAt *metav1.Time `json:"etcdRestartedAt,omitempty"`

	// etcdRebooting is the control plane whose reboot was requested and is not yet over; empty between
	// two reboots.
	// +optional
	EtcdRebooting string `json:"etcdRebooting,omitempty"`

	// etcdRebootBootID is the boot ID etcdRebooting had when its reboot was requested; the reboot has
	// happened once the node reports another one. Not a secret.
	// +optional
	EtcdRebootBootID string `json:"etcdRebootBootID,omitempty"`

	// apiServerInstances is, for each control plane the current stage was applied to in a rotation that
	// restarts kube-apiserver (the Kubernetes or the aggregator CA), the kube-apiserver container it ran in
	// just before the apply. The stage is verified once that control plane runs another one and answers
	// /readyz, so the operation is not Completed while a restart is still to come. Emptied when the stage
	// changes. Not a secret.
	// +optional
	APIServerInstances map[string]string `json:"apiServerInstances,omitempty"`

	// appliedAt is when the current stage was last applied to a machine; a kube-apiserver whose container did
	// not change within a minute of it is taken as not restarted by this stage.
	// +optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`
}

// CARotationCertificate is one rotated CA. Fingerprints are the SHA-256 of the certificate's DER,
// hex encoded; none of this is secret.
type CARotationCertificate struct {
	// name is talos (the Talos API CA), kubernetes (the Kubernetes API CA), etcd (the etcd CA) or
	// kubernetes-aggregator (the aggregator, or front-proxy, CA).
	// +required
	Name string `json:"name"`

	// previous is the CA the rotation started from.
	// +required
	Previous string `json:"previous"`

	// next is the new CA, whose key is in the bundle Secret's rotation.yaml until the rotation
	// is finalized.
	// +required
	Next string `json:"next"`

	// issuing is the CA that issues certificates in the current stage.
	// +optional
	Issuing string `json:"issuing,omitempty"`

	// accepted lists the CAs, besides the issuing one, that nodes accept in the current stage.
	// +optional
	Accepted []string `json:"accepted,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=top
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterRef.name"
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=".spec.kind"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Paused",type=boolean,JSONPath=".spec.paused"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosOperation is the Schema for the talosoperations API. It is a record
// of an imperative action in progress, not a declarative desired state.
type TalosOperation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosOperation
	// +required
	Spec TalosOperationSpec `json:"spec"`

	// status defines the observed state of TalosOperation
	// +optional
	Status TalosOperationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosOperationList contains a list of TalosOperation
type TalosOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosOperation `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosOperation{}, &TalosOperationList{})
		return nil
	})
}
