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
	// does not know it yet (ADR 0011); approval must set it before the
	// configuration reconciler can render anything for this machine.
	// +kubebuilder:validation:Enum=controlplane;worker
	// +optional
	Role string `json:"role,omitempty"`

	// nodeLabels are applied to the Kubernetes Node, not to this resource, which is why the field isn't called "labels" (ADR 0004).
	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// hostname overrides the Kubernetes Node name; Talos derives one from the machine if unset.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// approved allows the controller to render and apply configuration to this machine.
	// +kubebuilder:default=false
	// +optional
	Approved bool `json:"approved,omitempty"`
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
// +kubebuilder:printcolumn:name="Approved",type=boolean,JSONPath=".spec.approved"
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
