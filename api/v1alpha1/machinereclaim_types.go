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

// MachineReclaimPhase is where a reclaim is.
// +kubebuilder:validation:Enum=Inspecting;AwaitingConfirmation;Resetting;WaitingForMaintenance;Done;Failed;Expired
type MachineReclaimPhase string

const (
	MachineReclaimInspecting            MachineReclaimPhase = "Inspecting"
	MachineReclaimAwaitingConfirmation  MachineReclaimPhase = "AwaitingConfirmation"
	MachineReclaimResetting             MachineReclaimPhase = "Resetting"
	MachineReclaimWaitingForMaintenance MachineReclaimPhase = "WaitingForMaintenance"
	MachineReclaimDone                  MachineReclaimPhase = "Done"
	MachineReclaimFailed                MachineReclaimPhase = "Failed"
	MachineReclaimExpired               MachineReclaimPhase = "Expired"
)

// Names shared by the controller, the webhook and the UI API for the break-glass credential.
// ReclaimCredentialSecretType is the Secret type of the foreign talosconfig. Its only key is
// ReclaimCredentialKey.
const ReclaimCredentialSecretType corev1.SecretType = "ichor.maesh.dev/foreign-talosconfig"

const (
	// ReclaimCredentialKey is the credential Secret's only key.
	ReclaimCredentialKey = "talosconfig"
	// ReclaimLabel marks a credential Secret; the sweeper selects by it.
	ReclaimLabel = "ichor.maesh.dev/reclaim"
	// ReclaimRequestedByAnnotation and ReclaimConfirmedByAnnotation carry the Kubernetes user that
	// created the request and that wrote confirmUUID. The writer sets them; the webhook rejects a
	// value that is not the authenticated user, so they cannot be forged. The controller copies
	// them into status.audit.
	ReclaimRequestedByAnnotation = "ichor.maesh.dev/reclaim-requested-by"
	ReclaimConfirmedByAnnotation = "ichor.maesh.dev/reclaim-confirmed-by"
	// ReclaimAddedAddressesAnnotation is set on a TalosCluster by the UI API: the comma-separated
	// addresses it appended to spec.discovery.addresses for a reclaim request. Only those are ever
	// taken out again; an address the user added is never listed here.
	ReclaimAddedAddressesAnnotation = "ichor.maesh.dev/reclaim-added-addresses"
	// ReclaimVerb is the custom RBAC verb on machinereclaims checked by SubjectAccessReview.
	ReclaimVerb = "reclaim"
)

// MachineReclaimSpec is a one-shot, imperative request to wipe a machine that another Talos cluster
// configured, so that it returns to maintenance mode. The controller never
// writes it; the UI API writes confirmUUID for the user who typed it.
// +kubebuilder:validation:XValidation:rule="self.address == oldSelf.address && self.credentialSecretRef == oldSelf.credentialSecretRef",message="address and credentialSecretRef cannot change; make a new request"
type MachineReclaimSpec struct {
	// address is the one node to reclaim: an IP or host:port of its Talos API. Only this address is
	// dialled; the endpoints in the talosconfig are ignored.
	// +kubebuilder:validation:MinLength=1
	// +required
	Address string `json:"address"`

	// credentialSecretRef names a Secret of type ichor.maesh.dev/foreign-talosconfig in this
	// namespace. It is deleted when the request ends or expires.
	// +kubebuilder:validation:MinLength=1
	// +required
	CredentialSecretRef string `json:"credentialSecretRef"`

	// contextName picks the talosconfig context; empty is the file's default.
	// +optional
	ContextName string `json:"contextName,omitempty"`

	// clusterRef optionally names the TalosCluster (same namespace) the machine is meant to join
	// after the reset. The controller never writes it or that cluster; the UI API adds the address
	// to the cluster's discovery addresses on the user's behalf when it creates the request.
	// +optional
	ClusterRef *corev1.LocalObjectReference `json:"clusterRef,omitempty"`

	// confirmUUID is the node's UUID as typed by the user. Empty until then; the webhook rejects a
	// value that differs from status.inspection.uuid.
	// +optional
	ConfirmUUID string `json:"confirmUUID,omitempty"`

	// confirmClusterName is the foreign cluster's name as typed by the user; only needed with force.
	// +optional
	ConfirmClusterName string `json:"confirmClusterName,omitempty"`

	// force allows a control plane whose removal breaks the foreign etcd quorum or is the last of
	// its cluster. It also needs confirmClusterName.
	// +optional
	Force bool `json:"force,omitempty"`

	// forceUngraceful sends a non-graceful reset, for a foreign cluster that is dead.
	// +optional
	ForceUngraceful bool `json:"forceUngraceful,omitempty"`

	// ttl is how long the request and its credential live. Default 15 minutes, at most 60.
	// +kubebuilder:default="15m"
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s') && duration(self) <= duration('60m')",message="ttl must be between 1s and 60m"
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`
}

// MachineReclaimVolume is an orphaned volume the reset does not wipe.
type MachineReclaimVolume struct {
	Name      string `json:"name"`
	Location  string `json:"location,omitempty"`
	SizeBytes uint64 `json:"sizeBytes,omitempty"`
	// encrypted volumes lose their key with the reset; their data becomes unreadable.
	Encrypted bool `json:"encrypted,omitempty"`
}

// MachineReclaimEtcdMember is one member of the foreign cluster's etcd.
type MachineReclaimEtcdMember struct {
	ID       uint64 `json:"id"`
	Hostname string `json:"hostname,omitempty"`
	// healthy is false when the member did not answer its health check.
	Healthy bool `json:"healthy"`
}

// MachineReclaimInspection is what the read-only look at the node found. It holds no credential.
type MachineReclaimInspection struct {
	UUID             string                     `json:"uuid,omitempty"`
	Hostname         string                     `json:"hostname,omitempty"`
	Role             string                     `json:"role,omitempty"`
	TalosVersion     string                     `json:"talosVersion,omitempty"`
	ClusterName      string                     `json:"clusterName,omitempty"`
	ClusterID        string                     `json:"clusterID,omitempty"`
	ClusterReachable bool                       `json:"clusterReachable,omitempty"`
	Graceful         bool                       `json:"graceful,omitempty"`
	NeedsForce       bool                       `json:"needsForce,omitempty"`
	EtcdMembers      []MachineReclaimEtcdMember `json:"etcdMembers,omitempty"`
	Volumes          []MachineReclaimVolume     `json:"volumes,omitempty"`
	// refusals are why the reset cannot be sent; empty means it can, once confirmed.
	Refusals []string `json:"refusals,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// MachineReclaimAudit records who did what; it outlives the Events.
type MachineReclaimAudit struct {
	// requestedBy is the Kubernetes user that created the request, recorded by the webhook.
	RequestedBy string `json:"requestedBy,omitempty"`
	// confirmedBy is the user that wrote confirmUUID, recorded by the webhook.
	ConfirmedBy string `json:"confirmedBy,omitempty"`
	// confirmedAt is when the controller acted on the confirmation.
	ConfirmedAt *metav1.Time `json:"confirmedAt,omitempty"`
	// resetSentAt is set before the reset is sent; its presence refuses a second reset.
	ResetSentAt *metav1.Time `json:"resetSentAt,omitempty"`
	// credentialDeletedAt is when the controller deleted the Secret.
	CredentialDeletedAt *metav1.Time `json:"credentialDeletedAt,omitempty"`
}

// MachineReclaimStatus is the observed state.
type MachineReclaimStatus struct {
	// phase is where the request is.
	// +optional
	Phase MachineReclaimPhase `json:"phase,omitempty"`

	// inspection is the last read-only look at the node.
	// +optional
	Inspection *MachineReclaimInspection `json:"inspection,omitempty"`

	// expiresAt is creation time plus ttl; after it the credential is gone and nothing is sent.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// result is the new maintenance address, or the scan hint, or why the request failed.
	// +optional
	Result string `json:"result,omitempty"`

	// audit records who requested and confirmed.
	// +optional
	Audit MachineReclaimAudit `json:"audit,omitzero"`

	// conditions represent the current state of the request.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Address",type=string,JSONPath=`.spec.address`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MachineReclaim is the Schema for the machinereclaims API
type MachineReclaim struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of MachineReclaim
	// +required
	Spec MachineReclaimSpec `json:"spec"`

	// status defines the observed state of MachineReclaim
	// +optional
	Status MachineReclaimStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// MachineReclaimList contains a list of MachineReclaim
type MachineReclaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []MachineReclaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &MachineReclaim{}, &MachineReclaimList{})
		return nil
	})
}
