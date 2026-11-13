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

// Condition type and reasons on TalosEtcdBackup.status.conditions.
const (
	// TalosEtcdBackupConditionReady is True while the newest snapshot is
	// fresher than twice the schedule interval, False when the last attempt
	// failed or the newest snapshot is stale.
	TalosEtcdBackupConditionReady = "Ready"

	// TalosEtcdBackupConditionStoreInCluster is True (a warning, not a failure) while the destination
	// is inside the cluster being backed up: an etcd restore could not finish, because the restore
	// marker is written to the store after the Kubernetes API, and with it the store, is down.
	TalosEtcdBackupConditionStoreInCluster = "StoreInCluster"
	TalosEtcdBackupReasonInCluster         = "InCluster"

	TalosEtcdBackupReasonSucceeded = "Succeeded"
	TalosEtcdBackupReasonFailed    = "Failed"
	TalosEtcdBackupReasonStale     = "Stale"
	TalosEtcdBackupReasonSuspended = "Suspended"
)

// BackupNowAnnotation, set by `ichor backup etcd` (or the UI) to an RFC 3339 time, asks the
// controller for one snapshot outside the schedule. The controller never changes spec, so the request
// is an annotation; it acts once the time is newer than status.lastManualBackup.
const BackupNowAnnotation = "ichor.maesh.dev/backup-now"

// TalosEtcdBackupSpec defines the desired state of TalosEtcdBackup: a
// schedule, one S3-compatible destination and a retention count. Snapshots
// are always encrypted; the key is derived from the cluster's secrets bundle, so there is nothing to configure and
// no key to hand over.
// Holds references to Secrets only, never values, so it is safe in Git.
// +kubebuilder:validation:XValidation:rule="self.clusterRef == oldSelf.clusterRef",message="clusterRef cannot change after creation"
type TalosEtcdBackupSpec struct {
	// clusterRef names the TalosCluster (same namespace) whose etcd is backed up.
	// +kubebuilder:validation:XValidation:rule="self.name != ''",message="clusterRef.name is required"
	// +required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`

	// schedule is a standard five-field cron expression, evaluated in UTC.
	// +kubebuilder:default="0 * * * *"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// suspend stops new snapshots; existing ones are kept and not pruned.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// retention is how many snapshots to keep under the destination prefix.
	// +kubebuilder:default={keepLast: 24}
	// +optional
	Retention TalosEtcdBackupRetention `json:"retention,omitzero"`

	// destination is where snapshots are stored.
	// +required
	Destination TalosEtcdBackupDestination `json:"destination"`
}

// TalosEtcdBackupRetention bounds how many snapshots are kept.
type TalosEtcdBackupRetention struct {
	// keepLast keeps the newest N snapshots and deletes older ones after a
	// successful upload.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=24
	// +optional
	KeepLast int32 `json:"keepLast,omitempty"`
}

// TalosEtcdBackupDestination selects the storage backend. S3 is the only one.
type TalosEtcdBackupDestination struct {
	// s3 is an S3-compatible bucket.
	// +required
	S3 TalosEtcdBackupS3 `json:"s3"`
}

// TalosEtcdBackupS3 addresses an S3-compatible bucket.
type TalosEtcdBackupS3 struct {
	// endpoint is the service URL, e.g. https://s3.eu-west-1.amazonaws.com or
	// http://minio.minio.svc:9000. The scheme selects TLS.
	// +kubebuilder:validation:Pattern=`^https?://[^/\s]+$`
	// +required
	Endpoint string `json:"endpoint"`

	// insecure permits a plain http:// endpoint. The access key id, the signed headers
	// and the session token then cross the network in clear, so the admission webhook refuses
	// http:// unless this is true. Changing it needs update on talosetcdbackups/secret-references,
	// like the endpoint. Unset (false) means https only.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// region is the bucket's region; empty lets the client detect it.
	// +optional
	Region string `json:"region,omitempty"`

	// bucket is the bucket name. It must already exist.
	// +kubebuilder:validation:MinLength=3
	// +required
	Bucket string `json:"bucket"`

	// prefix is the key prefix snapshots are written under, with no leading
	// or trailing slash. Snapshots of different clusters need different prefixes.
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`
	// +required
	Prefix string `json:"prefix"`

	// credentialsSecretRef names a Secret holding accessKeyID and
	// secretAccessKey (and optionally sessionToken, and ca.crt, a PEM bundle
	// that signs the endpoint). It is read from the controller's own
	// namespace, not the namespace of this object.
	// +required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`
}

// TalosEtcdBackupSnapshot describes one stored snapshot. Nothing secret.
type TalosEtcdBackupSnapshot struct {
	// key is the object key within the bucket.
	// +required
	Key string `json:"key"`
	// time is when the snapshot was taken.
	// +required
	Time metav1.Time `json:"time"`
	// size is the stored (encrypted) size in bytes.
	// +required
	Size int64 `json:"size"`
	// sourceMachine is the TalosMachine the snapshot was read from.
	// +required
	SourceMachine string `json:"sourceMachine"`
}

// TalosEtcdBackupStored is one snapshot kept in the destination, as the last listing saw it.
type TalosEtcdBackupStored struct {
	// name is the object name under the prefix: what an etcd-restore operation's spec.etcdRestore.snapshot says.
	// +required
	Name string `json:"name"`
	// time is when the snapshot was taken (encoded in its name).
	// +required
	Time metav1.Time `json:"time"`
	// size is the stored (encrypted) size in bytes.
	// +required
	Size int64 `json:"size"`
}

// TalosEtcdBackupStatus defines the observed state of TalosEtcdBackup.
type TalosEtcdBackupStatus struct {
	// snapshots lists the snapshots kept in the destination after the last backup and prune,
	// newest first, so the UI and CLI can offer them for a restore without reading the bucket.
	// +kubebuilder:validation:MaxItems=500
	// +listType=atomic
	// +optional
	Snapshots []TalosEtcdBackupStored `json:"snapshots,omitempty"`

	// lastSnapshot is the newest snapshot this controller stored.
	// +optional
	LastSnapshot *TalosEtcdBackupSnapshot `json:"lastSnapshot,omitempty"`

	// lastAttemptTime is when the newest attempt started, successful or not.
	// +optional
	LastAttemptTime *metav1.Time `json:"lastAttemptTime,omitempty"`

	// lastSuccessTime is when the newest successful attempt finished.
	// +optional
	LastSuccessTime *metav1.Time `json:"lastSuccessTime,omitempty"`

	// lastManualBackup is the time of the newest backup-now request (BackupNowAnnotation) the
	// controller acted on; a request is acted on once, whether the snapshot succeeded or not.
	// +optional
	LastManualBackup *metav1.Time `json:"lastManualBackup,omitempty"`

	// consecutiveFailures counts failed attempts since the last success.
	// +optional
	ConsecutiveFailures int32 `json:"consecutiveFailures,omitempty"`

	// conditions: Ready.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterRef.name"
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=".spec.schedule"
// +kubebuilder:printcolumn:name="Last Success",type=date,JSONPath=".status.lastSuccessTime"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"

// TalosEtcdBackup is the Schema for the talosetcdbackups API
type TalosEtcdBackup struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TalosEtcdBackup
	// +required
	Spec TalosEtcdBackupSpec `json:"spec"`

	// status defines the observed state of TalosEtcdBackup
	// +optional
	Status TalosEtcdBackupStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TalosEtcdBackupList contains a list of TalosEtcdBackup
type TalosEtcdBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TalosEtcdBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &TalosEtcdBackup{}, &TalosEtcdBackupList{})
		return nil
	})
}
