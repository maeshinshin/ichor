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

// TalosClusterEndpoint is the cluster's control-plane endpoint.
type TalosClusterEndpoint struct {
	// type is the kind of address the endpoint resolves through.
	// +kubebuilder:validation:Enum=vip;loadBalancer;dns;node
	// +required
	Type string `json:"type"`

	// address is the endpoint's address: an IP for vip/node, or a hostname for loadBalancer/dns.
	// +kubebuilder:validation:MinLength=1
	// +required
	Address string `json:"address"`
}

// TalosClusterSchematic references the Image Factory schematic used for this cluster's installer image.
type TalosClusterSchematic struct {
	// id is the Image Factory schematic ID.
	// +kubebuilder:validation:MinLength=1
	// +required
	ID string `json:"id"`
}

// TalosClusterControlPlane configures the control-plane topology.
type TalosClusterControlPlane struct {
	// count is the number of control-plane nodes; 2 and 4 are rejected because they don't improve etcd availability over 1 or 3.
	// +kubebuilder:validation:Enum=1;3;5
	// +required
	Count int32 `json:"count"`

	// schedulable controls whether control planes accept workloads; required with no default because nothing on the object can resolve an auto value before any machine exists (ADR 0008).
	// +kubebuilder:validation:Enum="true";"false"
	// +required
	Schedulable string `json:"schedulable"`
}

// TalosClusterSpec defines the desired state of TalosCluster.
type TalosClusterSpec struct {
	// endpoint is the cluster's control-plane endpoint.
	// +required
	Endpoint TalosClusterEndpoint `json:"endpoint"`

	// talosVersion is the Talos release this cluster renders configuration for.
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+\.\d+$`
	// +required
	TalosVersion string `json:"talosVersion"`

	// kubernetesVersion is the Kubernetes release this cluster runs.
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+\.\d+$`
	// +required
	KubernetesVersion string `json:"kubernetesVersion"`

	// versionContract is required rather than defaulted, so that a machinery bump cannot silently change what gets applied (ADR 0004, principle 4).
	// +kubebuilder:validation:Pattern=`^v\d+\.\d+$`
	// +required
	VersionContract string `json:"versionContract"`

	// schematic is the Image Factory schematic used for this cluster's installer image.
	// +optional
	Schematic *TalosClusterSchematic `json:"schematic,omitempty"`

	// secretsRef names the Secret holding the Talos, Kubernetes and etcd CA bundle, in this TalosCluster's namespace.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="secretsRef.name is required"
	// +required
	SecretsRef corev1.LocalObjectReference `json:"secretsRef"`

	// controlPlane configures the control-plane topology.
	// +required
	ControlPlane TalosClusterControlPlane `json:"controlPlane"`

	// driftPolicy controls what the reconciler does when a node's live configuration no
	// longer matches what Ichor last rendered for it, and nothing on Ichor's side (this
	// TalosCluster, a TalosConfigPatch, or the TalosMachine) has changed since Ichor last
	// confirmed the two matched: warnOnly leaves the node as it is and reports the drift
	// through a condition; autoCorrect re-applies, but only when every changed field is
	// reboot-free (pkg/fieldmeta) -- a reboot-requiring or install-time-only drift is always
	// just reported, regardless of this setting. Defaults to warnOnly: unlike
	// controlPlane.schedulable (ADR 0008), the two values here are not wrong half the
	// time each -- silently changing a node is the one that can surprise, so it is never
	// the default.
	// +kubebuilder:validation:Enum=warnOnly;autoCorrect
	// +kubebuilder:default=warnOnly
	// +optional
	DriftPolicy string `json:"driftPolicy,omitempty"`
}

// TalosClusterStatus defines the observed state of TalosCluster.
type TalosClusterStatus struct {
	// conditions represent the current state of the TalosCluster resource, kstatus-compatible.
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
// +kubebuilder:resource:shortName=tcl
// +kubebuilder:printcolumn:name="Talos",type=string,JSONPath=".spec.talosVersion"
// +kubebuilder:printcolumn:name="Kubernetes",type=string,JSONPath=".spec.kubernetesVersion"
// +kubebuilder:printcolumn:name="Control Plane",type=integer,JSONPath=".spec.controlPlane.count"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TalosCluster is the Schema for the talosclusters API
type TalosCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosCluster
	// +required
	Spec TalosClusterSpec `json:"spec"`

	// status defines the observed state of TalosCluster
	// +optional
	Status TalosClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosClusterList contains a list of TalosCluster
type TalosClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TalosCluster{}, &TalosClusterList{})
		return nil
	})
}
