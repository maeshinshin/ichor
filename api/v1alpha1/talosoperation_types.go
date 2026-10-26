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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// order that keeps the Kubernetes API up as long as possible (ADR 0067).
	// Unlike the upgrade kinds it is not a rollout: TalosOperationReconciler
	// dispatches it to its own state machine, and while it has not
	// Completed every other disruptive operation on the cluster is refused.
	TalosOperationKindShutdown = "shutdown"

	// TalosOperationKindCARotation replaces the Talos API CA and the Kubernetes API CA in the three
	// passes of ADR 0063 (accept the new CA, make it the issuer, drop the old one). Like shutdown it
	// runs its own state machine, held in status.caRotation, instead of the per-machine rollout.
	TalosOperationKindCARotation = "ca-rotation"

	// TalosOperationKindConfig applies the pending reboot-requiring configuration changes of the
	// approved machines, one machine at a time through the node-operation hook, a staged apply and
	// a reboot (ADR 0080). Nothing reboots a node for a configuration change without one.
	TalosOperationKindConfig = "config"

	// TalosOperationKindNode runs one action on exactly one machine (spec.node, ADR 0085): a
	// reboot, a shutdown, a service restart, an etcd defragmentation or leadership handover, or a
	// reset. Like shutdown it runs its own state machine, held in status.node.
	TalosOperationKindNode = "node"
)

// Values for TalosOperationNodeSpec.Action.
const (
	NodeActionReboot                = "reboot"
	NodeActionShutdown              = "shutdown"
	NodeActionRestartService        = "restart-service"
	NodeActionEtcdDefrag            = "etcd-defrag"
	NodeActionEtcdForfeitLeadership = "etcd-forfeit-leadership"
	NodeActionReset                 = "reset"
)

// Values for TalosOperationNodeStatus.Step.
const (
	// NodeStepRequested: the action was sent to the node; the operation waits for its effect.
	NodeStepRequested = "Requested"

	// NodeStepRemoving: a reset handed the machine to the removal flow and waits for it to go.
	NodeStepRemoving = "Removing"
)

// Values for TalosOperationCARotationStatus.Stage, in the order they run. The stage names which CA
// every node is being given: it is the whole answer to "which CA is accepted and which issues".
const (
	// CARotationStageAccepting: the new CAs are added to every node's accepted CAs; the old CAs
	// still issue. The new CAs' keys are already in the bundle Secret's rotation.yaml.
	CARotationStageAccepting = "Accepting"

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

// ConditionBundleBackupStale is the TalosCluster condition that is True from the end of a CA
// rotation until the user has exported the new secrets bundle (ADR 0063).
const ConditionBundleBackupStale = "BundleBackupStale"

// BundleExportedAnnotation, set on a TalosCluster by `ichor cluster bundle-export` after it has saved
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
// +kubebuilder:validation:XValidation:rule="has(self.node) == has(oldSelf.node) && (!has(self.node) || self.node == oldSelf.node)",message="node cannot change after creation"
type TalosOperationSpec struct {
	// clusterRef names the TalosCluster this operation acts on.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// kind names which upgrade this is: talos (spec.talosVersion/schematic),
	// kubernetes (spec.kubernetesVersion), or ichor (the controller/CRDs/UI
	// install), cni (a spec.cni.kubeProxy switch, ADR 0066), shutdown (a
	// planned stop of the whole cluster, ADR 0067), or ca-rotation (the Talos API and
	// Kubernetes API CAs, ADR 0063). This CRD and its controller only
	// store and report it -- each kind's own execution is a separate unit's work.
	// shutdown and ca-rotation are the exceptions: each runs its own state machine.
	// config applies the pending reboot-requiring configuration changes, one machine at a time
	// with a reboot each (ADR 0080).
	// +kubebuilder:validation:Enum=talos;kubernetes;ichor;cni;shutdown;ca-rotation;config;node
	// +required
	Kind string `json:"kind"`

	// node is the action of a node-kind operation: which machine and what to do to it.
	// +optional
	Node *TalosOperationNodeSpec `json:"node,omitempty"`

	// paused stops the operation from advancing any further machine. It
	// does not undo a machine already advanced, and it does not release
	// the cluster lock -- the operation is not finished, only halted.
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

// TalosOperationNodeSpec is one action on one machine (ADR 0085). One action per operation.
// +kubebuilder:validation:XValidation:rule="self.action == 'restart-service' ? has(self.service) : !has(self.service)",message="service is required for restart-service and only allowed there"
// +kubebuilder:validation:XValidation:rule="self.action != 'reset' || (has(self.confirm) && self.confirm == self.machineRef.name)",message="reset needs confirm equal to the machine name"
// +kubebuilder:validation:XValidation:rule="!self.allowLeader || self.action == 'etcd-defrag'",message="allowLeader only applies to etcd-defrag"
type TalosOperationNodeSpec struct {
	// machineRef names the TalosMachine the action runs on.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="machineRef.name is required"
	// +required
	MachineRef corev1.LocalObjectReference `json:"machineRef"`

	// action is what to do: reboot, shutdown, restart-service, etcd-defrag (control planes),
	// etcd-forfeit-leadership (the control plane that leads etcd), or reset (the removal flow with
	// the node wiped, ADR 0042). etcd member removal is not an action: it is part of removal (ADR 0032).
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

	// node is the node kind's state machine.
	// +optional
	Node *TalosOperationNodeStatus `json:"node,omitempty"`

	// caRotation is the ca-rotation kind's state machine: which stage every node is being taken
	// to, and which CA is accepted and which issues in it. Public fingerprints only.
	// +optional
	CARotation *TalosOperationCARotationStatus `json:"caRotation,omitempty"`

	// conditions represent the current state of the TalosOperation resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TalosOperationCARotationStatus is the whole state of a CA rotation. Nothing else holds it: a
// restarted controller continues from stage, and per-node progress inside a stage is re-derived
// from what each node reports.
type TalosOperationCARotationStatus struct {
	// stage is the stage every node is being taken to; see the CARotationStage* constants.
	// Empty until the new CAs have been generated and saved.
	// +kubebuilder:validation:Enum=Accepting;Issuing;Dropping;Finalizing;Done
	// +optional
	Stage string `json:"stage,omitempty"`

	// certificates lists the two rotated CAs and, for the current stage, which fingerprint issues
	// and which are accepted.
	// +listType=map
	// +listMapKey=name
	// +optional
	Certificates []CARotationCertificate `json:"certificates,omitempty"`
}

// CARotationCertificate is one rotated CA. Fingerprints are the SHA-256 of the certificate's DER,
// hex encoded; none of this is secret.
type CARotationCertificate struct {
	// name is talos (the Talos API CA) or kubernetes (the Kubernetes API CA).
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
// of an imperative action in progress, not a declarative desired state, so
// it is one of the kinds docs/design/architecture.md's "GitOps and Argo CD"
// section names as not going in Git ("What doesn't [go in Git]: Secrets,
// TalosOperations, ..."); `ichor gitops export` excluding it is that other
// command's own responsibility (ADR 0026), not something this type can
// enforce on its own.
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
		s.AddKnownTypes(SchemeGroupVersion, &TalosOperation{}, &TalosOperationList{})
		return nil
	})
}
