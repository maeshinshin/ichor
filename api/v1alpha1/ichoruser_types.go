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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// IchorUserSpec defines the desired state of IchorUser (ADR 0094 Decision 1). There is no
// password, hash, token or session field on purpose: the hash lives in the Secret
// ichor-user-credentials, so a user CR can be committed to Git next to the other CRs.
type IchorUserSpec struct {
	// displayName is the human-readable name shown in the UI.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// email is informational only; nothing is sent to it and it is not an identity.
	// +kubebuilder:validation:MaxLength=254
	// +optional
	Email string `json:"email,omitempty"`

	// roles names the groups the user is in: each becomes the Kubernetes group ichor:<role>
	// (viewer, operator and admin are the built-in ones).
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9-]{0,30}$`
	// +listType=set
	// +optional
	Roles []string `json:"roles,omitempty"`

	// disabled refuses every login and refresh for this user without deleting it.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// Condition types written by the controller on IchorUser.status.conditions.
const (
	// IchorUserConditionReady is True when the user can log in.
	IchorUserConditionReady = "Ready"
	// IchorUserConditionPasswordSet is False for a user whose credentials Secret has no entry
	// (for example one applied from Git) until an admin sets a password.
	IchorUserConditionPasswordSet = "PasswordSet"
)

// IchorUserStatus defines the observed state of IchorUser. Only the controller writes it.
type IchorUserStatus struct {
	// conditions: Ready and PasswordSet.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the most recent generation the controller has reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=iusr
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z]([-a-z0-9]*[a-z0-9])?$') && size(self.metadata.name) <= 63",message="the name is the user's identity: a lower-case DNS label of at most 63 characters"
// +kubebuilder:printcolumn:name="Roles",type=string,JSONPath=".spec.roles"
// +kubebuilder:printcolumn:name="Disabled",type=boolean,JSONPath=".spec.disabled"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// IchorUser is the Schema for the ichorusers API. Its name is the identity: it becomes the
// token subject and the Kubernetes user ichor:<name>, and it cannot change (a rename is a new
// object).
type IchorUser struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of IchorUser
	// +required
	Spec IchorUserSpec `json:"spec"`

	// status defines the observed state of IchorUser
	// +optional
	Status IchorUserStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// IchorUserList contains a list of IchorUser
type IchorUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []IchorUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &IchorUser{}, &IchorUserList{})
		return nil
	})
}
