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

// TalosCredentialPurpose is what a TalosCredentialRequest asks the controller for.
// +kubebuilder:validation:Enum=public;node-reader;support-bundle;etcd-snapshot;packet-capture;credentials-talos;credentials-kubernetes;talosconfig-reader
type TalosCredentialPurpose string

const (
	// CredentialPurposePublic returns the cluster's public facts (status.public); it needs no CSR.
	CredentialPurposePublic TalosCredentialPurpose = "public"
	// CredentialPurposeNodeReader is an os:reader Talos client certificate for ichor-ui's own reads.
	CredentialPurposeNodeReader TalosCredentialPurpose = "node-reader"
	// CredentialPurposeSupportBundle, ...EtcdSnapshot and ...PacketCapture are the certificates of the
	// three admin-gated downloads, each with the least Talos role the call needs.
	CredentialPurposeSupportBundle TalosCredentialPurpose = "support-bundle"
	CredentialPurposeEtcdSnapshot  TalosCredentialPurpose = "etcd-snapshot"
	CredentialPurposePacketCapture TalosCredentialPurpose = "packet-capture"
	// CredentialPurposeCredentialsTalos and ...CredentialsKubernetes are the operator's talosconfig and
	// kubeconfig certificates; during a CA rotation they are signed by the new CAs and trust
	// both.
	CredentialPurposeCredentialsTalos      TalosCredentialPurpose = "credentials-talos"
	CredentialPurposeCredentialsKubernetes TalosCredentialPurpose = "credentials-kubernetes"
	// CredentialPurposeTalosconfigReader is the os:reader talosconfig a person takes for talosctl: one hour by default,
	// twelve at most, for whoever may `download` on
	// talosclusters/talosconfig-reader.
	CredentialPurposeTalosconfigReader TalosCredentialPurpose = "talosconfig-reader"
)

const (
	// CredentialIssueVerb is the custom RBAC verb the webhook checks on talosclusters/public and
	// talosclusters/node-reader for the two purposes only ichor-ui's ServiceAccount needs.
	CredentialIssueVerb = "issue"

	// CredentialPhaseIssued, ...Failed are the values of status.phase; an empty phase is pending.
	CredentialPhaseIssued = "Issued"
	CredentialPhaseFailed = "Failed"
)

// TalosCredentialRequestSpec asks the controller, which alone reads the secrets bundle, to sign one
// client certificate. The requester keeps the private key: it sends a PKCS#10 request and gets the
// certificate back, so nothing secret passes through this object.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="a credential request cannot change; make a new one"
// +kubebuilder:validation:XValidation:rule="self.purpose == 'public' || (has(self.csr) && size(self.csr) > 0)",message="csr is required for every purpose except public"
// +kubebuilder:validation:XValidation:rule="!(self.purpose in ['support-bundle', 'etcd-snapshot', 'packet-capture']) || has(self.machineRef)",message="machineRef is required for a download purpose"
type TalosCredentialRequestSpec struct {
	// clusterRef names the TalosCluster, in this namespace, whose CAs sign the certificate.
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// purpose decides the subject, the CA, the Talos role and the maximum lifetime, and which
	// Kubernetes permission the requester needs (checked by the admission webhook).
	// +required
	Purpose TalosCredentialPurpose `json:"purpose"`

	// machineRef names the TalosMachine a download purpose is for; the requester's permission is
	// checked on that machine.
	// +optional
	MachineRef *corev1.LocalObjectReference `json:"machineRef,omitempty"`

	// csr is a PEM PKCS#10 certificate request. Only its public key is used (its signature proves the
	// requester holds the key); the subject and any SANs are ignored and replaced.
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	CSR string `json:"csr,omitempty"`

	// ttl is the lifetime asked for. Empty means the purpose's default (10 minutes for a dial
	// certificate, one year for the operator credentials); the controller refuses more than the
	// purpose's cap.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// talosRole is os:reader or os:admin for purpose credentials-talos; empty means admin. It is
	// ignored by every other purpose.
	// +kubebuilder:validation:Enum=reader;admin
	// +optional
	TalosRole string `json:"talosRole,omitempty"`
}

// TalosCredentialPublic is what purpose public returns: facts that are public by nature.
type TalosCredentialPublic struct {
	// clusterID is the Talos cluster ID.
	// +optional
	ClusterID string `json:"clusterID,omitempty"`
	// talosCA, kubernetesCA, etcdCA and kubernetesAggregatorCA are the CA certificates (PEM).
	// +optional
	TalosCA string `json:"talosCA,omitempty"`
	// +optional
	KubernetesCA string `json:"kubernetesCA,omitempty"`
	// +optional
	EtcdCA string `json:"etcdCA,omitempty"`
	// +optional
	KubernetesAggregatorCA string `json:"kubernetesAggregatorCA,omitempty"`
}

// TalosCredentialRequestStatus is the controller's answer. It holds public material only.
type TalosCredentialRequestStatus struct {
	// phase is Issued or Failed; empty while the controller has not answered.
	// +optional
	Phase string `json:"phase,omitempty"`

	// message says why a request failed.
	// +optional
	Message string `json:"message,omitempty"`

	// certificate is the signed client certificate (PEM) for the key in spec.csr.
	// +optional
	Certificate string `json:"certificate,omitempty"`

	// trust is the CA certificate(s) (PEM) the certificate's API must be verified against: the
	// current CA, or the old and the new one during a CA rotation.
	// +optional
	Trust string `json:"trust,omitempty"`

	// notAfter is when the certificate expires.
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`

	// public is set for purpose public.
	// +optional
	Public *TalosCredentialPublic `json:"public,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Purpose",type=string,JSONPath=`.spec.purpose`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// TalosCredentialRequest is a short-lived request for one certificate. The controller deletes it a
// couple of minutes after it was created.
type TalosCredentialRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the request.
	// +required
	Spec TalosCredentialRequestSpec `json:"spec"`

	// status is the answer.
	// +optional
	Status TalosCredentialRequestStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosCredentialRequestList contains a list of TalosCredentialRequest
type TalosCredentialRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosCredentialRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosCredentialRequest{}, &TalosCredentialRequestList{})
		return nil
	})
}
