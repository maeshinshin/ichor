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

// TalosMachineIdentity identifies the physical machine backing a TalosMachine.
type TalosMachineIdentity struct {
	// uuid is the machine's SMBIOS UUID, the primary key for identity.
	// +kubebuilder:validation:MinLength=1
	// +required
	UUID string `json:"uuid"`

	// serial is the machine's SMBIOS serial number, used as an identity hint.
	// +optional
	Serial string `json:"serial,omitempty"`

	// macAddresses are the machine's NIC MAC addresses, used as an identity hint.
	// +optional
	MACAddresses []string `json:"macAddresses,omitempty"`
}

// Values for TalosMachineSpec.ApprovalState.
const (
	// MachineApprovalStatePending is the default: discovered, awaiting a
	// human decision. The controller renders and applies nothing for it.
	MachineApprovalStatePending = "pending"

	// MachineApprovalStateApproved lets the controller render and apply
	// configuration (TalosMachineReconciler's own gate, via IsApproved).
	MachineApprovalStateApproved = "approved"

	// MachineApprovalStateRejected means an operator explicitly turned this
	// machine away. It is distinct from pending so a rejected machine does
	// not keep showing up as a fresh approval request: ListMachines reports it, but it is never recreated
	// by discovery (which keys on identity, not approval state) and never
	// rendered. Re-approving a rejected machine is allowed and reverses it.
	MachineApprovalStateRejected = "rejected"
)

// TalosMachineSpec defines the desired state of TalosMachine.
type TalosMachineSpec struct {
	// clusterRef names the TalosCluster this machine belongs to.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// identity identifies the physical machine backing this resource.
	// +required
	Identity TalosMachineIdentity `json:"identity"`

	// endpoint is the machine's Talos API address.
	// +kubebuilder:validation:MinLength=1
	// +required
	Endpoint string `json:"endpoint"`

	// role is the machine's role in the cluster. Left unset by discovery, which
	// does not know it yet; approval must set it before the
	// configuration reconciler can render anything for this machine.
	// +kubebuilder:validation:Enum=controlplane;worker
	// +optional
	Role string `json:"role,omitempty"`

	// nodeLabels are applied to the Kubernetes Node, not to this resource, which is why the field isn't called "labels".
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// hostname overrides the Kubernetes Node name; Talos derives one from the machine if unset.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// approvalState records the human decision on this machine: pending (the
	// default, awaiting a decision), approved (the controller renders and
	// applies configuration), or rejected (an operator explicitly turned it
	// away; re-approving reverses it). This replaces an
	// earlier plain "approved" bool, which had no way to represent
	// "rejected" without a second field claiming the same truth -- one
	// field is the source of truth for this machine's approval status.
	// +kubebuilder:validation:Enum=pending;approved;rejected
	// +kubebuilder:default=pending
	// +optional
	ApprovalState string `json:"approvalState,omitempty"`

	// approvalDecision records who made the current approvalState decision,
	// when, and why. It is kept separate from approvalState, not folded
	// into it, so approvalState alone remains this machine's single source
	// of truth for approval status -- this struct asserts no approved/
	// rejected state of its own, only the record behind whichever state
	// approvalState already carries. Unset until the first approve/reject.
	// +optional
	ApprovalDecision *TalosMachineApprovalDecision `json:"approvalDecision,omitempty"`
}

// TalosMachineApprovalDecision is who decided a TalosMachine's current
// approvalState, when, and why -- not a second place that state can be
// read from (see TalosMachineSpec.ApprovalDecision's own comment).
type TalosMachineApprovalDecision struct {
	// actor identifies who made the decision. Fixed to the backend's own
	// identity until OIDC login lands; the field is added now
	// rather than later so every decision from here on has an actor
	// recorded, instead of leaving earlier decisions with none once a
	// real per-person identity exists to fill it.
	// +optional
	Actor string `json:"actor,omitempty"`

	// reason is optional free text, symmetric between approve and reject --
	// AuditRecord's own wire comment already documents "reason" as "mainly
	// used for rejections," not exclusive to them.
	// +optional
	Reason string `json:"reason,omitempty"`

	// at is when the decision was made.
	// +optional
	At metav1.Time `json:"at,omitempty"`
}

// IsApproved reports whether the controller may render and apply
// configuration to this machine -- TalosMachineReconciler's own gate, and
// the one place that check should be made rather than comparing
// ApprovalState's string value directly.
func (s TalosMachineSpec) IsApproved() bool {
	return s.ApprovalState == MachineApprovalStateApproved
}

// TalosMachineStatus defines the observed state of TalosMachine.
type TalosMachineStatus struct {
	// conditions represent the current state of the TalosMachine resource, kstatus-compatible.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// observedGeneration is the most recent generation the controller has reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// appliedConfigHash is the sha256 of the rendered configuration last confirmed applied to
	// the node (pkg/render's hash format), used for the convergence check on later reconciles.
	// It is a hash only, never the rendered configuration itself (principles 1 and 3).
	// +optional
	AppliedConfigHash string `json:"appliedConfigHash,omitempty"`

	// observedInputsHash hashes the generations of every object that fed the rendered
	// configuration last confirmed to match the node: the TalosCluster, this TalosMachine,
	// and every TalosConfigPatch that applies to it. It is only updated when the node is
	// confirmed converged, so comparing it against the current generations is how the
	// reconciler tells a desired-state change (a generation moved) apart from drift (none
	// did, so the node must have). A hash only, never the generations or config themselves
	// (principle 3).
	// +optional
	ObservedInputsHash string `json:"observedInputsHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tm
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".spec.role"
// +kubebuilder:printcolumn:name="Approval",type=string,JSONPath=".spec.approvalState"
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=".spec.endpoint"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosMachine is the Schema for the talosmachines API
type TalosMachine struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosMachine
	// +required
	Spec TalosMachineSpec `json:"spec"`

	// status defines the observed state of TalosMachine
	// +optional
	Status TalosMachineStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosMachineList contains a list of TalosMachine
type TalosMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TalosMachine{}, &TalosMachineList{})
		return nil
	})
}
