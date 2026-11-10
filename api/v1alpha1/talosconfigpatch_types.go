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

	"github.com/maeshinshin/ichor/pkg/typedconfig"
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

// TalosConfigPatchDocument carries exactly one Talos config document, restricted to Ichor's
// allow-list of kinds. kind is a discriminator: it says which of the fields below is
// populated, the same way a Talos config document's own "kind" field says what the rest of the
// document is. mirrorgen's roots (internal/tools/mirrorgen/main.go) names the same kinds this
// enum does; TestAllowListMatchesCRDEnum there checks the two agree, since a kubebuilder marker
// is plain text and can't reference that Go value directly.
// +kubebuilder:validation:XValidation:rule="(has(self.sysctlConfig) ? 1 : 0) + (has(self.kernelModuleConfig) ? 1 : 0) + (has(self.config) ? 1 : 0) + (has(self.volumeConfig) ? 1 : 0) + (has(self.userVolumeConfig) ? 1 : 0) + (has(self.rawVolumeConfig) ? 1 : 0) + (has(self.resolverConfig) ? 1 : 0) == 1",message="exactly one of sysctlConfig, kernelModuleConfig, config, volumeConfig, userVolumeConfig, rawVolumeConfig or resolverConfig must be set"
// +kubebuilder:validation:XValidation:rule="self.kind == 'SysctlConfig' ? has(self.sysctlConfig) : self.kind == 'KernelModuleConfig' ? has(self.kernelModuleConfig) : self.kind == 'Config' ? has(self.config) : self.kind == 'VolumeConfig' ? has(self.volumeConfig) : self.kind == 'UserVolumeConfig' ? has(self.userVolumeConfig) : self.kind == 'RawVolumeConfig' ? has(self.rawVolumeConfig) : has(self.resolverConfig)",message="kind must match the populated field"
// +kubebuilder:object:generate=false
type TalosConfigPatchDocument struct {
	// kind selects which of the fields below is populated.
	// +kubebuilder:validation:Enum=SysctlConfig;KernelModuleConfig;Config;VolumeConfig;UserVolumeConfig;RawVolumeConfig;ResolverConfig
	// +unionDiscriminator
	// +required
	Kind string `json:"kind"`

	// sysctlConfig is a Talos SysctlConfig document; populated when kind is "SysctlConfig".
	// +optional
	SysctlConfig *typedconfig.SysctlConfigV1Alpha1 `json:"sysctlConfig,omitempty"`

	// kernelModuleConfig is a Talos KernelModuleConfig document; populated when kind is "KernelModuleConfig".
	// +optional
	KernelModuleConfig *typedconfig.KernelModuleConfigV1Alpha1 `json:"kernelModuleConfig,omitempty"`

	// config is a Talos v1alpha1 Config document; populated when kind is "Config".
	// +optional
	Config *typedconfig.Config `json:"config,omitempty"`

	// volumeConfig is a Talos VolumeConfig document (a system volume, e.g. STATE or EPHEMERAL,
	// placed on its own disk); populated when kind is "VolumeConfig".
	// +optional
	VolumeConfig *typedconfig.VolumeConfigV1Alpha1 `json:"volumeConfig,omitempty"`

	// userVolumeConfig is a Talos UserVolumeConfig document; populated when kind is "UserVolumeConfig".
	// +optional
	UserVolumeConfig *typedconfig.UserVolumeConfigV1Alpha1 `json:"userVolumeConfig,omitempty"`

	// rawVolumeConfig is a Talos RawVolumeConfig document (an unformatted partition for a CSI
	// provisioner); populated when kind is "RawVolumeConfig".
	// +optional
	RawVolumeConfig *typedconfig.RawVolumeConfigV1Alpha1 `json:"rawVolumeConfig,omitempty"`

	// resolverConfig is a Talos ResolverConfig document (DNS nameservers and search domains); populated when kind is "ResolverConfig".
	// +optional
	ResolverConfig *typedconfig.ResolverConfigV1Alpha1 `json:"resolverConfig,omitempty"`
}

// DeepCopyInto copies the receiver into out. in must be non-nil.
func (in *TalosConfigPatchDocument) DeepCopyInto(out *TalosConfigPatchDocument) {
	*out = *in
	if in.SysctlConfig != nil {
		in, out := &in.SysctlConfig, &out.SysctlConfig
		*out = new(typedconfig.SysctlConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
	if in.KernelModuleConfig != nil {
		in, out := &in.KernelModuleConfig, &out.KernelModuleConfig
		*out = new(typedconfig.KernelModuleConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
	if in.Config != nil {
		in, out := &in.Config, &out.Config
		*out = new(typedconfig.Config)
		(*in).DeepCopyInto(*out)
	}
	if in.VolumeConfig != nil {
		in, out := &in.VolumeConfig, &out.VolumeConfig
		*out = new(typedconfig.VolumeConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
	if in.UserVolumeConfig != nil {
		in, out := &in.UserVolumeConfig, &out.UserVolumeConfig
		*out = new(typedconfig.UserVolumeConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
	if in.RawVolumeConfig != nil {
		in, out := &in.RawVolumeConfig, &out.RawVolumeConfig
		*out = new(typedconfig.RawVolumeConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
	if in.ResolverConfig != nil {
		in, out := &in.ResolverConfig, &out.ResolverConfig
		*out = new(typedconfig.ResolverConfigV1Alpha1)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy returns a copy of the receiver.
func (in *TalosConfigPatchDocument) DeepCopy() *TalosConfigPatchDocument {
	if in == nil {
		return nil
	}

	out := new(TalosConfigPatchDocument)
	in.DeepCopyInto(out)

	return out
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
