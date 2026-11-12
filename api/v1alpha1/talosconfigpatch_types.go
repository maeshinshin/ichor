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

// TalosConfigPatchTarget narrows which machines a patch applies to; exactly one field must be set.
type TalosConfigPatchTarget struct {
	// role targets every machine with this role.
	// +kubebuilder:validation:Enum=controlplane;worker
	// +optional
	Role string `json:"role,omitempty"`

	// labelSelector targets machines whose Kubernetes Node labels match.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// machineRef targets a single named TalosMachine.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="machineRef.name is required"
	// +optional
	MachineRef *corev1.LocalObjectReference `json:"machineRef,omitempty"`
}

// TalosConfigPatchSpec defines the desired state of TalosConfigPatch.
// +kubebuilder:validation:XValidation:rule="!has(self.target) || (has(self.target.role) ? 1 : 0) + (has(self.target.labelSelector) ? 1 : 0) + (has(self.target.machineRef) ? 1 : 0) == 1",message="target must set exactly one of role, labelSelector or machineRef"
// +kubebuilder:validation:XValidation:rule="self.layer == 'cluster' ? !has(self.target) : has(self.target)",message="layer 'cluster' requires target to be absent; layer 'role', 'selector' or 'machine' requires target to be present"
// +kubebuilder:validation:XValidation:rule="!has(self.target) || (self.layer == 'role' ? has(self.target.role) : (self.layer == 'selector' ? has(self.target.labelSelector) : (self.layer == 'machine' ? has(self.target.machineRef) : true)))",message="the target must match the layer: layer 'role' takes target.role, 'selector' takes target.labelSelector, 'machine' takes target.machineRef"
type TalosConfigPatchSpec struct {
	// clusterRef names the TalosCluster this patch applies to.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// layer is where this patch applies in the bottom-up stack; "system" is absent because Ichor generates that layer
	// itself.
	// +kubebuilder:validation:Enum=cluster;role;selector;machine
	// +required
	Layer string `json:"layer"`

	// priority orders patches within the same layer.
	// +kubebuilder:default=0
	// +optional
	Priority int32 `json:"priority"`

	// target narrows which machines this patch applies to; required for every layer but "cluster", and its kind follows the layer: role -> role, selector -> labelSelector, machine -> machineRef.
	// +optional
	Target *TalosConfigPatchTarget `json:"target,omitempty"`

	// documents holds the Talos config documents this patch applies, restricted to an allow-list of kinds.
	// +optional
	Documents []TalosConfigPatchDocument `json:"documents,omitempty"`
}

// TalosConfigPatchStatus defines the observed state of TalosConfigPatch.
type TalosConfigPatchStatus struct {
	// conditions represent the current state of the TalosConfigPatch resource, kstatus-compatible.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// observedGeneration is the most recent generation the controller has reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=tcp
// +kubebuilder:printcolumn:name="Layer",type=string,JSONPath=".spec.layer"
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=".spec.priority"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosConfigPatch is the Schema for the talosconfigpatches API
type TalosConfigPatch struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosConfigPatch
	// +required
	Spec TalosConfigPatchSpec `json:"spec"`

	// status defines the observed state of TalosConfigPatch
	// +optional
	Status TalosConfigPatchStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosConfigPatchList contains a list of TalosConfigPatch
type TalosConfigPatchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosConfigPatch `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosConfigPatch{}, &TalosConfigPatchList{})
		return nil
	})
}
