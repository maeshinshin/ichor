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

// TalosStorageSpec defines the desired state of TalosStorage. It stays thin on purpose (design
// section "Storage"): which nodes, which disk preset, how disks are handed to the storage
// system, and evacuation settings. The storage system's own configuration (Longhorn's Helm
// values, replica counts, and so on) lives in Argo CD, not here -- Ichor only owns layer 1
// (Talos-side preparation) and layer 4 (node-operation coordination, a separate work unit).
type TalosStorageSpec struct {
	// clusterRef names the TalosCluster this storage configuration applies to.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// nodes selects which nodes get storage prepared, by their Kubernetes Node labels.
	// +required
	Nodes metav1.LabelSelector `json:"nodes"`

	// diskPreset names which storage system this configuration prepares Talos for. Each preset
	// fixes the required system extensions, the UserVolumeConfig shape, and the kubelet mount
	// that preset needs; see pkg/storage.
	// +kubebuilder:validation:Enum=longhorn-v1
	// +required
	DiskPreset string `json:"diskPreset"`

	// diskSelector is a Talos disk-selector CEL expression (the same language
	// provisioning.diskSelector.match uses in a VolumeConfig/UserVolumeConfig/RawVolumeConfig
	// document) choosing which disk on each selected node is handed to the storage system.
	// +kubebuilder:validation:XValidation:rule="self != ''",message="diskSelector is required"
	// +required
	DiskSelector string `json:"diskSelector"`

	// evacuationTimeout bounds how long the node-operation hook (layer 4) waits for replicas to
	// evacuate a node before pausing the operation rather than proceeding. Unset means the hook's
	// own default.
	// +optional
	EvacuationTimeout *metav1.Duration `json:"evacuationTimeout,omitempty"`
}

// TalosStorageStatus defines the observed state of TalosStorage.
type TalosStorageStatus struct {
	// conditions represent the current state of the TalosStorage resource, kstatus-compatible.
	// Ready reports whether the Talos side (layer 1) is prepared, so Argo CD's sync wave can hold
	// the storage system's Application until this is true (design section "Storage").
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
// +kubebuilder:resource:shortName=tsto
// +kubebuilder:printcolumn:name="Preset",type=string,JSONPath=".spec.diskPreset"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosStorage is the Schema for the talosstorages API
type TalosStorage struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosStorage
	// +required
	Spec TalosStorageSpec `json:"spec"`

	// status defines the observed state of TalosStorage
	// +optional
	Status TalosStorageStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosStorageList contains a list of TalosStorage
type TalosStorageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosStorage `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosStorage{}, &TalosStorageList{})
		return nil
	})
}
