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
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TalosOperationKindKeyRotation replaces the Kubernetes service-account signing key and the kube-apiserver
// secretbox (encryption at rest) key through a dual-key period. Like ca-rotation it runs its own
// state machine, held in status.keyRotation, instead of the per-machine rollout.
const TalosOperationKindKeyRotation = "key-rotation"

// The keys spec.keyRotation.targets can name.
const (
	KeyRotationTargetServiceAccount = "serviceAccount"
	KeyRotationTargetSecretbox      = "secretbox"
)

// Values for TalosOperationKeyRotationStatus.Stage, in the order they run. The stage names what every
// control plane is being given: which key signs or encrypts and which are accepted besides it.
const (
	// KeyRotationStageAccepting: the new service-account public key is accepted for verification and the
	// new secretbox key is a second provider that only decrypts; the old keys still sign and encrypt. The
	// new keys are already in the bundle Secret's keyrotation.yaml.
	KeyRotationStageAccepting = "Accepting"

	// KeyRotationStageIssuing: the new service-account key signs and the new secretbox key encrypts; the
	// old keys are still accepted and still decrypt.
	KeyRotationStageIssuing = "Issuing"

	// KeyRotationStageReencrypting: every Secret is rewritten so that it is stored under the new secretbox
	// key; the stage ends after a sweep that had nothing left to rewrite. Skipped when secretbox is not a target.
	KeyRotationStageReencrypting = "Reencrypting"

	// KeyRotationStageSettlingTokens: the old service-account key is kept while bound tokens signed by it are
	// re-issued, and until no legacy token Secret depends on it. Skipped when serviceAccount is not a target.
	KeyRotationStageSettlingTokens = "SettlingTokens"

	// KeyRotationStageDropping: the old keys are removed from every control plane.
	KeyRotationStageDropping = "Dropping"

	// KeyRotationStageFinalizing: every control plane runs on the new keys alone. The bundle Secret's
	// secrets.yaml is replaced, the retired keys recorded and keyrotation.yaml removed, then every machine is
	// checked against the replaced bundle.
	KeyRotationStageFinalizing = "Finalizing"

	// KeyRotationStageDone: the rotation is complete.
	KeyRotationStageDone = "Done"
)

// DefaultKeyRotationTokenSettle is how long a key rotation keeps the old service-account key after the new
// one signs when spec.keyRotation.tokenSettle is not set: a projected token lives one hour and the kubelet
// re-requests it after 80% of that, so every token of a running pod is re-issued within 48 minutes.
const DefaultKeyRotationTokenSettle = 90 * time.Minute

// MinKeyRotationTokenSettle is the shortest spec.keyRotation.tokenSettle a key rotation accepts (hardware
// amendment). The tokens kube-apiserver puts in a pod's projected volume are asked for with
// expirationSeconds 3607 and the kubelet re-requests one at 80% of that (48 minutes) plus up to ten seconds of
// jitter, so a pod that is running holds a token signed by the new key within about 49 minutes of the
// new key signing. Dropping the old key earlier makes the tokens still held, those of Ichor's own controller
// and of the CNI among them, fail with 401. One hour for the token plus 15 minutes for a slow kubelet.
const MinKeyRotationTokenSettle = 75 * time.Minute

// TokenSettleRefusal is why spec.keyRotation.tokenSettle is too short, or "". It applies only when the
// service-account key is rotated; an unset tokenSettle takes the default and is never refused.
func (s *TalosOperationKeyRotationSpec) TokenSettleRefusal() string {
	if s == nil || s.TokenSettle == nil || s.TokenSettle.Duration >= MinKeyRotationTokenSettle ||
		!slices.Contains(s.Targets, KeyRotationTargetServiceAccount) {
		return ""
	}

	return fmt.Sprintf("tokenSettle %s is shorter than the %s a key rotation needs: pods hold projected ServiceAccount tokens "+
		"signed by the old key for up to 48 minutes (the kubelet re-requests a one-hour token at 80%% of its life), and "+
		"dropping the old key before that makes Ichor's own controller, the CNI and every other pod that still holds one fail "+
		"with 'the server has asked for the client to provide credentials' until the kubelet refreshes it. "+
		"Use %s or longer (the default is %s); raise it further when workloads request projected tokens that live longer than an hour",
		s.TokenSettle.Duration, MinKeyRotationTokenSettle, MinKeyRotationTokenSettle, DefaultKeyRotationTokenSettle)
}

// TalosOperationKeyRotationSpec says which keys a key-rotation operation replaces and how patient it is.
type TalosOperationKeyRotationSpec struct {
	// targets names the keys to replace: serviceAccount (the key that signs ServiceAccount tokens),
	// secretbox (the key Secrets are encrypted with in etcd), or both.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:items:Enum=serviceAccount;secretbox
	// +listType=set
	// +required
	Targets []string `json:"targets"`

	// tokenSettle is how long the old service-account key is kept after the new one signs, so that the
	// projected tokens of running pods are re-issued under the new one. The default is 90 minutes and the
	// shortest accepted is 75 minutes (a shorter one makes pods that still hold an old token fail until the
	// kubelet refreshes it). Raise it when workloads request projected tokens that live longer than an hour.
	// Ignored without the serviceAccount target.
	// +optional
	TokenSettle *metav1.Duration `json:"tokenSettle,omitempty"`

	// ignoreLegacyTokens lets the rotation drop the old service-account key although Secrets of type
	// kubernetes.io/service-account-token that it signed still exist. Those tokens stop working when the key
	// is dropped. By default the operation waits until none is left.
	// +kubebuilder:default=false
	// +optional
	IgnoreLegacyTokens bool `json:"ignoreLegacyTokens,omitempty"`
}

// TalosOperationKeyRotationStatus is the whole state of a key rotation. Nothing else holds it: a restarted
// controller continues from stage, and which control planes a stage still has to visit is re-derived from
// what each reports.
type TalosOperationKeyRotationStatus struct {
	// stage is the stage every control plane is being taken to; see the KeyRotationStage* constants. Empty
	// until the new keys have been generated and saved.
	// +kubebuilder:validation:Enum=Accepting;Issuing;Reencrypting;SettlingTokens;Dropping;Finalizing;Done
	// +optional
	Stage string `json:"stage,omitempty"`

	// keys lists the rotated keys and, for the current stage, which one signs or encrypts and which are
	// accepted. Public identifiers only; no key material.
	// +listType=map
	// +listMapKey=name
	// +optional
	Keys []KeyRotationKey `json:"keys,omitempty"`

	// restart is the control plane whose kube-apiserver the current stage restarted last, and the container
	// it ran before. The stage waits for another container and /readyz before it visits the next control plane.
	// +optional
	Restart *KeyRotationRestart `json:"restart,omitempty"`

	// reencryption is the progress of the Reencrypting stage.
	// +optional
	Reencryption *KeyRotationReencryption `json:"reencryption,omitempty"`

	// settlingSince is when the SettlingTokens stage started waiting for the projected tokens signed by the old
	// service-account key to be re-issued. The wait ends spec.keyRotation.tokenSettle after it.
	// +optional
	SettlingSince *metav1.Time `json:"settlingSince,omitempty"`
}

// KeyRotationKey is one rotated key. Identifiers are public: the SHA-256 of the service-account public key's
// DER, or a salted hash prefix of the secretbox secret.
type KeyRotationKey struct {
	// name is serviceAccount or secretbox.
	// +required
	Name string `json:"name"`

	// previous is the key the rotation started from.
	// +required
	Previous string `json:"previous"`

	// next is the new key, held in the bundle Secret's keyrotation.yaml until the rotation is finalized.
	// +required
	Next string `json:"next"`

	// active is the key that signs (serviceAccount) or encrypts (secretbox) in the current stage.
	// +optional
	Active string `json:"active,omitempty"`

	// accepted lists the keys, besides the active one, that verify or decrypt in the current stage.
	// +optional
	Accepted []string `json:"accepted,omitempty"`
}

// KeyRotationRestart records a kube-apiserver restart under way.
type KeyRotationRestart struct {
	// machine is the TalosMachine whose control plane was told to restart its kube-apiserver.
	// +required
	Machine string `json:"machine"`

	// instance is the kube-apiserver container that ran before the restart.
	// +optional
	Instance string `json:"instance,omitempty"`
}

// KeyRotationReencryption counts what the Reencrypting stage did.
type KeyRotationReencryption struct {
	// sweeps is how many complete passes over every Secret have run.
	// +optional
	Sweeps int32 `json:"sweeps,omitempty"`

	// rewritten is how many Secrets the last complete sweep stored again under the new key. The stage ends
	// after a sweep that rewrote none.
	// +optional
	Rewritten int64 `json:"rewritten,omitempty"`

	// scanned is how many Secrets the last complete sweep looked at.
	// +optional
	Scanned int64 `json:"scanned,omitempty"`
}

// KeyRotationOutdatesSnapshot reports whether op, a TalosOperation, is a key rotation that was not finished
// when a snapshot was taken: still under way, or finished after the snapshot's time. Such a snapshot holds
// Secrets, among them the secrets bundle, under keys the control planes no longer configure, so restoring
// it would leave Ichor unable to read its own bundle.
func KeyRotationOutdatesSnapshot(op *TalosOperation, snapshot time.Time) bool {
	if op.Spec.Kind != TalosOperationKindKeyRotation {
		return false
	}

	if op.Status.Phase != TalosOperationPhaseCompleted {
		// Not finished: it counts once it has changed a control plane.
		return op.Status.KeyRotation != nil && op.Status.KeyRotation.Stage != ""
	}

	return op.Status.FinishedAt != nil && snapshot.Before(op.Status.FinishedAt.Time)
}
