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
)

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
type TalosOperationSpec struct {
	// clusterRef names the TalosCluster this operation acts on.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// kind names which upgrade this is: talos (spec.talosVersion/schematic),
	// kubernetes (spec.kubernetesVersion), or ichor (the controller/CRDs/UI
	// install), cni (a spec.cni.kubeProxy switch, ADR 0066), or shutdown (a
	// planned stop of the whole cluster, ADR 0067). This CRD and its controller only
	// store and report it -- each kind's own execution is a separate unit's work.
	// shutdown is the exception: it runs its own state machine.
	// +kubebuilder:validation:Enum=talos;kubernetes;ichor;cni;shutdown
	// +required
	Kind string `json:"kind"`

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

	// conditions represent the current state of the TalosOperation resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
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
