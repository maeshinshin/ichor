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

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/robfig/cron/v3"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

const (
	// backupRetryInterval is how soon a failed backup is retried, instead of
	// waiting for the next scheduled tick.
	backupRetryInterval = 5 * time.Minute

	// backupTimeout bounds one attempt: dial, snapshot, encrypt, upload.
	backupTimeout = time.Hour

	// Keys of the S3 credentials Secret (TalosEtcdBackupS3.CredentialsSecretRef).
	s3KeyAccessKeyID     = "accessKeyID"
	s3KeySecretAccessKey = "secretAccessKey"
	s3KeySessionToken    = "sessionToken"
	s3KeyCA              = "ca.crt"
)

// Prometheus series for "failures and staleness surface as metrics and
// alerts" (design, "Backup, recovery, certificates"). Alert rules over them
// ship with the chart's monitoring option, not here.
var (
	backupLastSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ichor_etcd_backup_last_success_timestamp_seconds",
		Help: "Unix time of the newest stored etcd snapshot; alert on time() minus this.",
	}, []string{"namespace", "name"})
	backupFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ichor_etcd_backup_failures_total",
		Help: "Failed etcd backup attempts.",
	}, []string{"namespace", "name"})
)

func init() {
	metrics.Registry.MustRegister(backupLastSuccess, backupFailures)
}

// TalosEtcdBackupReconciler takes scheduled, verified, encrypted etcd
// snapshots into S3-compatible storage and prunes old ones. ADR 0047 records
// the format and the choices behind it.
//
// A snapshot is a read of one etcd member; it does not stop or slow etcd in a
// way that needs the cluster lock, so this reconciler never calls
// acquireDisruptiveLock (and acquire_guard_test keeps that the only Acquire
// site). If a node is mid-upgrade the snapshot of that node simply fails and
// the next control plane is tried.
type TalosEtcdBackupReconciler struct {
	client.Client

	// ControllerNamespace is where the bundle and S3 credentials Secrets are
	// read, never the namespace of the TalosEtcdBackup (ADR 0011).
	ControllerNamespace string

	// SnapshotClient dials one control plane.
	SnapshotClient EtcdSnapshotClientFactory

	// NewStore builds the S3 store; nil means etcdbackup.NewS3Store.
	NewStore func(etcdbackup.S3Config) (etcdbackup.Store, error)

	// SpoolDir is where the encrypted snapshot is staged before upload, so a
	// snapshot that fails verification is never uploaded. Must have room for one
	// snapshot. Only ciphertext is ever written there. Empty means os.TempDir().
	SpoolDir string

	// Now is time.Now unless a test sets it.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosetcdbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch

func (r *TalosEtcdBackupReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}

	return time.Now()
}

// Reconcile takes a snapshot when the schedule says one is due. Everything it
// needs to decide that is in status, so a restarted controller resumes.
func (r *TalosEtcdBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var bk ichorv1alpha1.TalosEtcdBackup
	if err := r.Get(ctx, req.NamespacedName, &bk); err != nil {
		if client.IgnoreNotFound(err) == nil {
			backupLastSuccess.DeleteLabelValues(req.Namespace, req.Name)
			backupFailures.DeleteLabelValues(req.Namespace, req.Name)
		}

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	r.noteStoreLocation(ctx, &bk)

	if bk.Spec.Suspend {
		return ctrl.Result{}, r.setReady(ctx, &bk, metav1.ConditionUnknown, ichorv1alpha1.TalosEtcdBackupReasonSuspended,
			"spec.suspend is true; no snapshots are taken and none are pruned")
	}

	sched, err := cron.ParseStandard(bk.Spec.Schedule)
	if err != nil {
		// A spec edit re-queues; retrying the same bad expression would not help.
		return ctrl.Result{}, r.setReady(ctx, &bk, metav1.ConditionFalse, ichorv1alpha1.TalosEtcdBackupReasonFailed,
			fmt.Sprintf("spec.schedule is not a valid cron expression: %v", err))
	}

	now := r.now()

	manual := consumeBackupNow(&bk)

	if wait := r.untilDue(&bk, sched, now); wait > 0 && !manual {
		r.refreshStaleness(&bk, sched, now)

		return ctrl.Result{RequeueAfter: wait}, r.Status().Update(ctx, &bk)
	}

	start := now
	log := logf.FromContext(ctx)

	var scrub scrubber

	snap, stored, pruneErr, err := r.runBackup(ctx, &bk, start, &scrub)

	bk.Status.LastAttemptTime = &metav1.Time{Time: start}

	if err != nil {
		msg := scrub.clean(err.Error())
		bk.Status.ConsecutiveFailures++
		backupFailures.WithLabelValues(bk.Namespace, bk.Name).Inc()
		log.Info("etcd backup failed", "namespace", bk.Namespace, "name", bk.Name, "error", msg)
		setReadyCondition(&bk, metav1.ConditionFalse, ichorv1alpha1.TalosEtcdBackupReasonFailed, msg)

		return ctrl.Result{RequeueAfter: backupRetryInterval}, r.Status().Update(ctx, &bk)
	}

	done := metav1.NewTime(r.now())
	bk.Status.LastSuccessTime = &done
	bk.Status.LastSnapshot = snap
	bk.Status.Snapshots = stored
	bk.Status.ConsecutiveFailures = 0
	backupLastSuccess.WithLabelValues(bk.Namespace, bk.Name).Set(float64(done.Unix()))

	msg := "stored " + snap.Key
	if pruneErr != nil {
		msg += "; pruning old snapshots failed: " + scrub.clean(pruneErr.Error())
	}

	setReadyCondition(&bk, metav1.ConditionTrue, ichorv1alpha1.TalosEtcdBackupReasonSucceeded, msg)

	return ctrl.Result{RequeueAfter: max(sched.Next(start).Sub(r.now()), time.Second)}, r.Status().Update(ctx, &bk)
}

// noteStoreLocation sets the warning condition StoreInCluster while the destination is inside the
// cluster that is backed up, and removes it otherwise. The caller writes status in the same reconcile.
func (r *TalosEtcdBackupReconciler) noteStoreLocation(ctx context.Context, bk *ichorv1alpha1.TalosEtcdBackup) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: bk.Namespace, Name: bk.Spec.ClusterRef.Name}, &cluster); err == nil {
		if reason := etcdbackup.InClusterReason(bk.Spec.Destination.S3.Endpoint, &cluster); reason != "" {
			apimeta.SetStatusCondition(&bk.Status.Conditions, metav1.Condition{
				Type: ichorv1alpha1.TalosEtcdBackupConditionStoreInCluster, Status: metav1.ConditionTrue,
				Reason: ichorv1alpha1.TalosEtcdBackupReasonInCluster, Message: etcdbackup.InClusterMessage(reason),
				ObservedGeneration: bk.Generation,
			})

			return
		}
	}

	apimeta.RemoveStatusCondition(&bk.Status.Conditions, ichorv1alpha1.TalosEtcdBackupConditionStoreInCluster)
}

// consumeBackupNow reports whether a backup-now request (BackupNowAnnotation) newer than
// status.lastManualBackup is waiting, and records it as acted on. A missing, unparsable or not-newer
// annotation is ignored. The caller writes status in the same reconcile.
func consumeBackupNow(bk *ichorv1alpha1.TalosEtcdBackup) bool {
	at, err := time.Parse(time.RFC3339, bk.Annotations[ichorv1alpha1.BackupNowAnnotation])
	if err != nil {
		return false
	}

	if last := bk.Status.LastManualBackup; last != nil && !at.After(last.Time) {
		return false
	}

	t := metav1.NewTime(at)
	bk.Status.LastManualBackup = &t

	return true
}

// untilDue returns 0 when a snapshot is due now.
func (r *TalosEtcdBackupReconciler) untilDue(bk *ichorv1alpha1.TalosEtcdBackup, sched cron.Schedule, now time.Time) time.Duration {
	if bk.Status.LastAttemptTime == nil {
		return 0
	}

	last := bk.Status.LastAttemptTime.Time

	due := sched.Next(last)
	if bk.Status.ConsecutiveFailures > 0 {
		due = last.Add(backupRetryInterval)
	}

	return max(due.Sub(now), 0)
}

// refreshStaleness flips Ready to False/Stale when the newest success is older
// than two schedule intervals. A failed state is left as it is.
func (r *TalosEtcdBackupReconciler) refreshStaleness(bk *ichorv1alpha1.TalosEtcdBackup, sched cron.Schedule, now time.Time) {
	if bk.Status.LastSuccessTime == nil || bk.Status.ConsecutiveFailures > 0 {
		return
	}

	next := sched.Next(bk.Status.LastSuccessTime.Time)
	interval := sched.Next(next).Sub(next)

	if now.Sub(bk.Status.LastSuccessTime.Time) > 2*interval+interval/2 {
		setReadyCondition(bk, metav1.ConditionFalse, ichorv1alpha1.TalosEtcdBackupReasonStale,
			"newest snapshot is older than two schedule intervals")
	}
}

// runBackup takes one snapshot end to end. Anything it returns as an error may
// contain what the S3 client or Talos echoed back, so the caller must pass it
// through scrub.clean before it reaches status or a log; scrub learns each
// secret as it is loaded.
func (r *TalosEtcdBackupReconciler) runBackup(
	ctx context.Context, bk *ichorv1alpha1.TalosEtcdBackup, start time.Time, scrub *scrubber,
) (snap *ichorv1alpha1.TalosEtcdBackupSnapshot, stored []ichorv1alpha1.TalosEtcdBackupStored, pruneErr, err error) {
	ctx, cancel := context.WithTimeout(ctx, backupTimeout)
	defer cancel()

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: bk.Namespace, Name: bk.Spec.ClusterRef.Name}, &cluster); err != nil {
		return nil, nil, nil, fmt.Errorf("getting TalosCluster %q: %w", bk.Spec.ClusterRef.Name, err)
	}

	bundle, passphrase, store, err := loadBackupStore(ctx, r.Client, r.ControllerNamespace, &cluster, bk.Spec.Destination.S3, r.NewStore, scrub)
	if err != nil {
		return nil, nil, nil, err
	}

	candidates, err := r.snapshotCandidates(ctx, &cluster, bundle)
	if err != nil {
		return nil, nil, nil, err
	}

	prefix := bk.Spec.Destination.S3.Prefix
	key := prefix + "/" + etcdbackup.ObjectName(start)

	var errs []error

	for _, m := range candidates {
		size, serr := r.snapshotAndUpload(ctx, &cluster, m, bundle, passphrase, store, key)
		if serr != nil {
			errs = append(errs, fmt.Errorf("machine %q: %w", m.Name, serr))

			continue
		}

		// max(…, 1): a zero value (CRD default not applied) must never mean "keep nothing".
		pruneErr = etcdbackup.Prune(ctx, store, prefix, int(max(bk.Spec.Retention.KeepLast, 1)))

		return &ichorv1alpha1.TalosEtcdBackupSnapshot{
			Key: key, Time: metav1.NewTime(start), Size: size, SourceMachine: m.Name,
		}, storedSnapshots(ctx, store, prefix), pruneErr, nil
	}

	return nil, nil, nil, errors.Join(errs...)
}

// maxStoredSnapshots is the CRD's cap on status.snapshots.
const maxStoredSnapshots = 500

// storedSnapshots lists what the destination holds after a backup and its prune, newest first. A
// failed listing yields nil: the list is for choosing a restore, and the next backup rebuilds it.
func storedSnapshots(ctx context.Context, store etcdbackup.Store, prefix string) []ichorv1alpha1.TalosEtcdBackupStored {
	objs, err := store.List(ctx, prefix)
	if err != nil {
		return nil
	}

	out := make([]ichorv1alpha1.TalosEtcdBackupStored, 0, min(len(objs), maxStoredSnapshots))

	for _, o := range objs[:min(len(objs), maxStoredSnapshots)] {
		out = append(out, ichorv1alpha1.TalosEtcdBackupStored{Name: o.Name, Time: metav1.NewTime(o.Time), Size: o.Size})
	}

	return out
}

// backupKey derives the passphrases of cluster's backups from bundle and from the secretbox secrets key
// rotations retired (ADR 0121), so backups sealed before a rotation stay readable. Every secret it touches is
// added to scrub.
func backupKey(
	ctx context.Context, c client.Reader, controllerNamespace string, cluster *ichorv1alpha1.TalosCluster,
	bundle *secrets.Bundle, scrub *scrubber,
) (etcdbackup.Key, error) {
	retired, err := secretsbundle.LoadRetired(ctx, c, controllerNamespace, cluster.Spec.SecretsRef.Name)
	if err != nil {
		return etcdbackup.Key{}, err
	}

	scrub.add(retired.Secretboxes()...)

	passphrase, err := etcdbackup.DeriveKey(bundle, retired.Secretboxes()...)
	if err != nil {
		return etcdbackup.Key{}, err
	}

	scrub.add(passphrase.Secrets()...)

	return passphrase, nil
}

// loadBackupStore loads what a backup and a restore both start from: the cluster's secrets bundle, the
// passphrase derived from it, and the opened object store (newStore nil means S3). Each secret is
// added to scrub as it is loaded.
func loadBackupStore(
	ctx context.Context, c client.Reader, controllerNamespace string, cluster *ichorv1alpha1.TalosCluster,
	dst ichorv1alpha1.TalosEtcdBackupS3, newStore func(etcdbackup.S3Config) (etcdbackup.Store, error), scrub *scrubber,
) (*secrets.Bundle, etcdbackup.Key, etcdbackup.Store, error) {
	bundle, err := loadBundle(ctx, c, controllerNamespace, cluster)
	if err != nil {
		return nil, etcdbackup.Key{}, nil, err
	}

	scrub.add(bundleSecretMaterial(bundle)...)

	passphrase, err := backupKey(ctx, c, controllerNamespace, cluster, bundle, scrub)
	if err != nil {
		return nil, etcdbackup.Key{}, nil, err
	}

	s3cfg, err := loadS3ConfigFrom(ctx, c, controllerNamespace, dst, scrub)
	if err != nil {
		return nil, etcdbackup.Key{}, nil, err
	}

	store, err := openStore(newStore, s3cfg)

	return bundle, passphrase, store, err
}

// openStore builds the object store; a nil newStore means S3.
func openStore(newStore func(etcdbackup.S3Config) (etcdbackup.Store, error), cfg etcdbackup.S3Config) (etcdbackup.Store, error) {
	if newStore == nil {
		newStore = etcdbackup.NewS3Store
	}

	return newStore(cfg)
}

// loadS3ConfigFrom reads the credentials Secret from the controller's namespace
// (ADR 0011, the same rule as loadBundle and render.ResolveRegistryAuth); the etcd restore
// (ADR 0089) reads the same credentials the backup wrote with. Errors name the Secret and key,
// never a value.
func loadS3ConfigFrom(
	ctx context.Context, c client.Reader, controllerNamespace string, dst ichorv1alpha1.TalosEtcdBackupS3, scrub *scrubber,
) (etcdbackup.S3Config, error) {
	if controllerNamespace == "" {
		return etcdbackup.S3Config{}, errors.New("loading S3 credentials: controller namespace is empty (is POD_NAMESPACE set?)")
	}

	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: controllerNamespace, Name: dst.CredentialsSecretRef.Name}, &sec); err != nil {
		return etcdbackup.S3Config{}, fmt.Errorf("getting S3 credentials Secret %q: %w", dst.CredentialsSecretRef.Name, err)
	}

	cfg := etcdbackup.S3Config{
		Endpoint: dst.Endpoint, Region: dst.Region, Bucket: dst.Bucket,
		AccessKeyID:     string(sec.Data[s3KeyAccessKeyID]),
		SecretAccessKey: string(sec.Data[s3KeySecretAccessKey]),
		SessionToken:    string(sec.Data[s3KeySessionToken]),
		CAPEM:           sec.Data[s3KeyCA],
	}

	scrub.add(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)

	for key, v := range map[string]string{s3KeyAccessKeyID: cfg.AccessKeyID, s3KeySecretAccessKey: cfg.SecretAccessKey} {
		if v == "" {
			return etcdbackup.S3Config{}, fmt.Errorf("S3 credentials Secret %q has no %q key", dst.CredentialsSecretRef.Name, key)
		}
	}

	return cfg, nil
}

// snapshotCandidates lists the approved control planes in the order to try
// them: healthy non-leaders first (the design's "healthy non-leader" -- the
// leader serves all writes, so reading a follower keeps load off it), healthy
// leaders next. An unreachable or unhealthy member is skipped.
//
// It lists through deleteguard.ApprovedMachines, to choose a snapshot source,
// not to count control planes: nothing is gated on the number, and a stale
// cached list only costs a failed dial before the next candidate.
func (r *TalosEtcdBackupReconciler) snapshotCandidates(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, bundle *secrets.Bundle,
) ([]*ichorv1alpha1.TalosMachine, error) {
	list, err := deleteguard.ApprovedMachines(ctx, r.Client, cluster.Namespace, cluster.Name,
		deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	var followers, leaders []*ichorv1alpha1.TalosMachine

	var errs []error

	for i := range list {
		m := &list[i]

		st, err := r.etcdStatus(ctx, cluster, m, bundle)
		if err != nil {
			errs = append(errs, fmt.Errorf("machine %q: etcd status: %w", m.Name, err))

			continue
		}

		switch {
		case st.IsLearner || len(st.Errors) > 0:
			errs = append(errs, fmt.Errorf("machine %q: etcd member is not healthy", m.Name))
		case st.MemberID == st.LeaderID:
			leaders = append(leaders, m)
		default:
			followers = append(followers, m)
		}
	}

	out := slices.Concat(followers, leaders)
	if len(out) == 0 {
		if len(errs) == 0 {
			return nil, errors.New("no approved control-plane machine to snapshot")
		}

		return nil, errors.Join(errs...)
	}

	return out, nil
}

func (r *TalosEtcdBackupReconciler) etcdStatus(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) (EtcdNodeStatus, error) {
	c, err := r.SnapshotClient(ctx, cluster, m, bundle)
	if err != nil {
		return EtcdNodeStatus{}, err
	}
	defer c.Close() //nolint:errcheck

	return c.EtcdStatus(ctx)
}

// snapshotAndUpload streams one node's snapshot through verification and
// encryption into a spool file, and uploads the file only if verification
// passed. Plaintext is never written to disk.
func (r *TalosEtcdBackupReconciler) snapshotAndUpload(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle, passphrase etcdbackup.Key, store etcdbackup.Store, key string,
) (int64, error) {
	c, err := r.SnapshotClient(ctx, cluster, m, bundle)
	if err != nil {
		return 0, err
	}
	defer c.Close() //nolint:errcheck

	rc, err := c.EtcdSnapshot(ctx)
	if err != nil {
		return 0, fmt.Errorf("requesting snapshot: %w", err)
	}
	defer rc.Close() //nolint:errcheck

	spool, err := os.CreateTemp(r.SpoolDir, "etcd-snapshot-*.age")
	if err != nil {
		return 0, fmt.Errorf("creating spool file: %w", err)
	}

	defer os.Remove(spool.Name()) //nolint:errcheck
	defer spool.Close()           //nolint:errcheck

	enc, err := etcdbackup.Encrypt(spool, passphrase)
	if err != nil {
		return 0, err
	}

	ver := etcdbackup.NewVerifier()

	if _, err := io.Copy(io.MultiWriter(ver, enc), rc); err != nil {
		return 0, fmt.Errorf("reading snapshot: %w", err)
	}

	if err := ver.Verify(); err != nil {
		return 0, err
	}

	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("encrypting snapshot: %w", err)
	}

	size, err := spool.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	if err := store.Put(ctx, key, spool, size); err != nil {
		return 0, fmt.Errorf("uploading snapshot: %w", err)
	}

	return size, nil
}

func setReadyCondition(bk *ichorv1alpha1.TalosEtcdBackup, status metav1.ConditionStatus, reason, msg string) {
	apimeta.SetStatusCondition(&bk.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.TalosEtcdBackupConditionReady, Status: status, Reason: reason, Message: msg,
		ObservedGeneration: bk.Generation,
	})
}

func (r *TalosEtcdBackupReconciler) setReady(
	ctx context.Context, bk *ichorv1alpha1.TalosEtcdBackup, status metav1.ConditionStatus, reason, msg string,
) error {
	setReadyCondition(bk, status, reason, msg)

	return r.Status().Update(ctx, bk)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosEtcdBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosEtcdBackup{}, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Named("talosetcdbackup").
		Complete(r)
}
