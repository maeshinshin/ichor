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

// This file implements the TalosOperationKindEtcdRestore state machine: put a stored etcd
// snapshot back into a running cluster. The sequence is Talos's own disaster-recovery procedure
// (the EtcdRecoverSuite integration test of Talos 1.14.3):
//
//	begin       take the cluster lock; check the snapshot (decrypt, checksum), pin a copy of it, reach every
//	            control plane; write the marker; start the job
//	Wiping      reset EPHEMERAL on every control plane (etcd data goes, STATE stays), non-gracefully, with
//	            reboot -- the job (etcd_restore_tail.go)
//	Recovering  upload the snapshot to one control plane and Bootstrap with RecoverEtcd -- the job
//	Rejoining   the other control planes join the new single-member etcd by themselves; wait for every
//	            member and for the Kubernetes API; the controller's own node is wiped here, last
//	Reconciling ask every TalosMachine to reconcile again and wait until each is Ready
//
// Nothing of this can live in the Kubernetes API alone: the restore takes the API away and rolls it
// back to the snapshot, which deletes this very TalosOperation (it was created after the snapshot).
// The progress is therefore kept in a marker object next to the backups, written before every
// irreversible step, and the operation is recreated from it once the API answers again.
package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

const (
	etcdRestoreFinalizer = "ichor.maesh.dev/etcd-restore"

	// serviceKubelet is the Talos service id of the kubelet.
	serviceKubelet = "kubelet"

	// etcdRestoreOverallTimeout bounds a restore from its start: the wipe and recovery, the rejoin of
	// the other control planes, and the machines' reconcile. Past it the operation pauses.
	etcdRestoreOverallTimeout = 150 * time.Minute

	// etcdRestoreWaitInterval is how often a started restore looks again.
	etcdRestoreWaitInterval = 15 * time.Second

	reasonEtcdRestoreDone = "EtcdRestored"

	// staleWipeConditionPrefix names the status condition (one per control plane) that holds when the
	// reset of a node with stale etcd data was last sent.
	staleWipeConditionPrefix = "StaleWipeSent-"
)

// etcdRestoreStepRank orders the steps; a marker and a status are compared by it.
func etcdRestoreStepRank(step string) int {
	return 1 + slices.Index([]string{
		ichorv1alpha1.EtcdRestoreStepWiping, ichorv1alpha1.EtcdRestoreStepRecovering,
		ichorv1alpha1.EtcdRestoreStepRejoining, ichorv1alpha1.EtcdRestoreStepReconciling,
	}, step) // not found is -1, so rank 0
}

// etcdRestoreStarted reports whether the destructive part of an etcd restore was recorded: from there
// the operation is driven without the cluster lock.
func etcdRestoreStarted(op *ichorv1alpha1.TalosOperation) bool {
	return op.Spec.Kind == ichorv1alpha1.TalosOperationKindEtcdRestore &&
		op.Status.EtcdRestore != nil && op.Status.EtcdRestore.Step != "" &&
		op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted
}

// restoreRun is what one reconcile of an etcd restore reads: the backup it restores from and the
// object store behind it.
type restoreRun struct {
	r          *TalosOperationReconciler
	op         *ichorv1alpha1.TalosOperation
	cluster    *ichorv1alpha1.TalosCluster
	bundle     *secrets.Bundle
	store      etcdbackup.Store
	prefix     string
	passphrase etcdbackup.Key
	scrub      scrubber
	backup     *ichorv1alpha1.TalosEtcdBackup
	warn       string // what did not converge on the nodes that were not wiped
}

// newRestoreRun reads the backup, the bundle and the S3 credentials of op. It reads through the
// manager's cache, which keeps answering while the API is away.
func (r *TalosOperationReconciler) newRestoreRun(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
) (*restoreRun, error) {
	spec := op.Spec.EtcdRestore
	if spec == nil {
		return nil, errors.New("an etcd-restore operation has no spec.etcdRestore")
	}

	run := &restoreRun{r: r, op: op, cluster: cluster}

	var bk ichorv1alpha1.TalosEtcdBackup
	if err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: spec.BackupRef.Name}, &bk); err != nil {
		return nil, fmt.Errorf("getting TalosEtcdBackup %q: %w", spec.BackupRef.Name, err)
	}

	if bk.Spec.ClusterRef.Name != cluster.Name {
		return nil, fmt.Errorf("TalosEtcdBackup %q backs up cluster %q, not %q", bk.Name, bk.Spec.ClusterRef.Name, cluster.Name)
	}

	bundle, passphrase, store, err := loadBackupStore(ctx, r.Client, r.ControllerNamespace, cluster, bk.Spec.Destination.S3, r.NewStore, &run.scrub)
	if err != nil {
		return nil, err
	}

	run.backup, run.bundle, run.store, run.prefix, run.passphrase = &bk, bundle, store, bk.Spec.Destination.S3.Prefix, passphrase

	return run, nil
}

func (run *restoreRun) clean(err error) string { return run.scrub.clean(err.Error()) }

// approved lists the approved machines of the run's cluster, live when an API reader is wired.
func (run *restoreRun) approved(ctx context.Context, f deleteguard.MachineFilter) ([]ichorv1alpha1.TalosMachine, error) {
	return deleteguard.ApprovedMachines(ctx, readerOrClient(run.r.APIReader, run.r.Client), run.cluster.Namespace, run.cluster.Name, f)
}

// ours reports whether the marker belongs to this operation.
func (run *restoreRun) ours(m *etcdbackup.RestoreMarker) bool {
	return m.Namespace == run.op.Namespace && m.Operation == run.op.Name
}

// restoreNodes is the plan for the control planes: which are wiped by the job, which receives the
// snapshot.
type restoreNodes struct {
	all    []*ichorv1alpha1.TalosMachine // every approved control plane, by name
	order  []*ichorv1alpha1.TalosMachine // wiped by the job
	target *ichorv1alpha1.TalosMachine
	// own is the control plane the controller runs on when it is not the target: it is wiped by the
	// reconcile after etcd is back. nil when the controller runs elsewhere, or on the only control plane.
	own *ichorv1alpha1.TalosMachine
	// controllerOnTarget: the controller runs on the target, the only control plane. The wipe stops
	// the controller, so the restore cannot finish by itself.
	controllerOnTarget bool
	// names are the control planes' Kubernetes node names by machine name (see nodeNameFor).
	names map[string]string
}

// nodeNameFor is m's Kubernetes node name: the resolved one (names, by machine name), else spec.hostname.
func nodeNameFor(names map[string]string, m *ichorv1alpha1.TalosMachine) string {
	if n := names[m.Name]; n != "" {
		return n
	}

	return m.Spec.Hostname
}

// planRestoreNodes decides who is wiped and who recovers. The node the controller runs on is wiped
// last, after the snapshot is recovered elsewhere, so that the controller lives through the recovery
// (its stopping at the very end costs nothing: the next controller continues from the marker). With a
// single control plane there is no elsewhere.
func planRestoreNodes(cps []*ichorv1alpha1.TalosMachine, nodeName string, names map[string]string) restoreNodes {
	all := slices.Clone(cps)
	slices.SortFunc(all, func(a, b *ichorv1alpha1.TalosMachine) int { return strings.Compare(a.Name, b.Name) })

	p := restoreNodes{all: all, names: names}

	var own *ichorv1alpha1.TalosMachine

	for _, m := range all {
		if nodeName != "" && nodeNameFor(names, m) == nodeName {
			own = m
		}
	}

	for _, m := range all {
		if m == own && len(all) > 1 {
			p.own = m

			continue
		}

		p.order = append(p.order, m)
	}

	if len(p.order) > 0 {
		p.target = p.order[0]
	}

	p.controllerOnTarget = own != nil && len(all) == 1

	return p
}

// restoreCandidates lists the approved control planes of the cluster through the same selection as every
// other count of them. The read is live: an operation that is about to wipe them must not act on a
// stale list.
func (r *TalosOperationReconciler) restoreCandidates(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) ([]*ichorv1alpha1.TalosMachine, error) {
	list, err := (&restoreRun{r: r, cluster: cluster}).approved(ctx, deleteguard.MachineFilter{Role: roleControlPlane})
	if err != nil {
		return nil, err
	}

	out := make([]*ichorv1alpha1.TalosMachine, len(list))
	for i := range list {
		out[i] = &list[i]
	}

	return out, nil
}

func (r *TalosOperationReconciler) restoreNode(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) restoreNode {
	cluster, m = cluster.DeepCopy(), m.DeepCopy() // the job outlives the reconcile

	return restoreNode{name: m.Name, dial: func(ctx context.Context) (TalosClient, error) {
		ctx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
		defer cancel()

		return r.TalosClient(ctx, cluster, m, bundle)
	}}
}

// refused pauses an operation that has changed nothing yet, and says so.
func (r *TalosOperationReconciler) restoreRefused(ctx context.Context, op *ichorv1alpha1.TalosOperation, run *restoreRun, err error) (ctrl.Result, error) {
	msg := err.Error()
	if run != nil {
		msg = run.clean(err)
	}

	return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationStepFailed,
		msg+" (nothing was changed)")
}

// beginEtcdRestore runs under the cluster lock and changes nothing until every check has passed.
//
//nolint:gocyclo // one list of independent checks
func (r *TalosOperationReconciler) beginEtcdRestore(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) (ctrl.Result, error) {
	if r.TalosClient == nil {
		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no Talos client is wired up for this controller")
	}

	run, err := r.newRestoreRun(ctx, op, cluster)
	if err != nil {
		return r.restoreRefused(ctx, op, nil, err)
	}

	spec := op.Spec.EtcdRestore

	// A marker that is already ours means the status write after it failed: carry on from the marker.
	marker, err := etcdbackup.GetMarker(ctx, run.store, run.prefix, run.passphrase)
	if err != nil {
		return r.restoreRefused(ctx, op, run, err)
	}

	if marker != nil {
		if run.ours(marker) {
			snap, started := metav1.NewTime(marker.SnapshotTime), metav1.NewTime(marker.StartedAt)
			op.Status.EtcdRestore = &ichorv1alpha1.TalosOperationEtcdRestoreStatus{
				Step: marker.Step, Target: marker.Target, SnapshotTime: &snap, RequestedAt: &started,
				Message: "carrying on from the restore marker.",
			}
			op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress

			if err := r.Status().Update(ctx, op); err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		return r.restoreRefused(ctx, op, run, fmt.Errorf("a restore by operation %s/%s is recorded in the backup storage (step %s): "+
			"finish it, or delete that operation to clear it", marker.Namespace, marker.Operation, marker.Step))
	}

	objs, err := run.store.List(ctx, run.prefix)
	if err != nil {
		return r.restoreRefused(ctx, op, run, fmt.Errorf("listing the backup storage: %w", err))
	}

	idx := slices.IndexFunc(objs, func(o etcdbackup.Object) bool { return o.Name == spec.Snapshot })
	if idx < 0 {
		return r.restoreRefused(ctx, op, run, fmt.Errorf("snapshot %q is not in the backup storage (it may have been pruned)", spec.Snapshot))
	}

	snap := objs[idx]

	if err := r.noRotationSince(ctx, cluster, snap.Time); err != nil {
		return r.restoreRefused(ctx, op, run, err)
	}

	cps, err := r.restoreCandidates(ctx, cluster)
	if err != nil {
		return r.restoreRefused(ctx, op, run, err)
	}

	if len(cps) == 0 {
		return r.restoreRefused(ctx, op, run, errors.New("the cluster has no approved control plane"))
	}

	// Every control plane must answer now: etcd is wiped on each, and a node that is away would come
	// back later with old data.
	for _, m := range cps {
		if err := r.restoreReachable(ctx, cluster, m, run.bundle); err != nil {
			return r.restoreRefused(ctx, op, run, err)
		}
	}

	// Which control plane is the controller's is read now, before anything is wiped (a wiped node can no
	// longer be asked), and kept in the marker. spec.hostname is empty on the first control plane.
	var names map[string]string

	if r.NodeName != "" {
		var err error
		if names, err = r.nodeNames(ctx, cluster, run.bundle, cps); err != nil {
			return r.restoreRefused(ctx, op, run, fmt.Errorf("cannot tell which control plane runs this controller (node %q): %w", r.NodeName, err))
		}
	}

	nodes := planRestoreNodes(cps, r.NodeName, names)

	if _, err := etcdbackup.VerifyObject(ctx, run.store, snap.Key, run.passphrase); err != nil {
		return r.restoreRefused(ctx, op, run, fmt.Errorf("snapshot %q does not pass its check: %w", snap.Name, err))
	}

	if err := lock.Renew(ctx, controllerIdentity); err != nil {
		return renewRefusal(ctx, err)
	}

	if err := etcdbackup.Pin(ctx, run.store, run.prefix, snap.Key, snap.Size); err != nil {
		return r.restoreRefused(ctx, op, run, err)
	}

	now := time.Now().UTC()
	next := etcdbackup.RestoreMarker{
		Namespace: op.Namespace, Operation: op.Name, Cluster: cluster.Name, Backup: spec.BackupRef.Name,
		Snapshot: snap.Name, SnapshotTime: snap.Time, Reason: spec.Reason, Target: nodes.target.Name,
		Step: ichorv1alpha1.EtcdRestoreStepWiping, StartedAt: now, Nodes: names,
	}

	if nodes.own != nil {
		next.Own = nodes.own.Name
	}

	// The marker is what makes the destructive part resumable. Nothing is wiped before it is stored.
	if err := etcdbackup.PutMarker(ctx, run.store, run.prefix, run.passphrase, &next); err != nil {
		_ = run.store.Delete(ctx, etcdbackup.PinnedKey(run.prefix))

		return r.restoreRefused(ctx, op, run, fmt.Errorf("writing the restore marker: %w", err))
	}

	t := metav1.NewTime(snap.Time)
	req := metav1.NewTime(now)
	op.Status.EtcdRestore = &ichorv1alpha1.TalosOperationEtcdRestoreStatus{
		Step: ichorv1alpha1.EtcdRestoreStepWiping, Target: nodes.target.Name, SnapshotTime: &t, RequestedAt: &req,
		Message: wipingMessage(nodes),
	}
	op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	op.Status.FailureReason = ""

	// A failed write is tolerable from here: the marker holds the truth, and the next reconcile mirrors it.
	if err := r.Status().Update(ctx, op); err != nil {
		logf.FromContext(ctx).Info("recording the restore start in the operation status failed; the marker holds it", "error", err.Error())
	}

	run.launch(&next, nodes)

	return ctrl.Result{RequeueAfter: etcdRestoreWaitInterval / 3}, nil
}

func wipingMessage(n restoreNodes) string {
	names := make([]string, len(n.order))
	for i, m := range n.order {
		names[i] = m.Name
	}

	msg := fmt.Sprintf("Wiping etcd data on %s, then recovering the snapshot on %s.", strings.Join(names, ", "), n.target.Name)

	switch {
	case n.own != nil:
		msg += fmt.Sprintf(" %s (node %s), where this controller runs, is wiped last, when etcd is back.", n.own.Name, nodeNameFor(n.names, n.own))
	case n.controllerOnTarget:
		msg += " This controller runs on the only control plane and stops when it is wiped: finish with `ichor restore --recover-cluster`."
	}

	return msg
}

// restoreReachable checks that m answers and that its etcd data can be read.
func (r *TalosOperationReconciler) restoreReachable(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
) error {
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	c, err := r.TalosClient(dialCtx, cluster, m, bundle)
	if err != nil {
		return fmt.Errorf("control plane %q cannot be reached (%v): a restore wipes etcd on every control plane, so every one must answer", m.Name, err)
	}

	defer closeTalosClient(c)

	if _, err := c.EtcdDataPresent(dialCtx); err != nil {
		return fmt.Errorf("control plane %q: reading its etcd data: %w", m.Name, err)
	}

	return nil
}

// noRotationSince refuses a restore whose snapshot is older than a CA rotation of the cluster: the
// restored secrets bundle would not match the certificates the nodes now hold, and Ichor could no
// longer reach them.
func (r *TalosOperationReconciler) noRotationSince(ctx context.Context, cluster *ichorv1alpha1.TalosCluster, snapshot time.Time) error {
	var ops ichorv1alpha1.TalosOperationList
	if err := r.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return err
	}

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation && o.Spec.ClusterRef.Name == cluster.Name &&
			o.CreationTimestamp.After(snapshot) {
			return fmt.Errorf("operation %q rotated the cluster's CAs after this snapshot was taken (%s): the restored secrets "+
				"bundle would not match the nodes' certificates; choose a snapshot taken after the rotation",
				o.Name, snapshot.UTC().Format(time.RFC3339))
		}
	}

	return nil
}

// launch starts the destructive job for marker m. The job needs nothing from Kubernetes.
func (run *restoreRun) launch(m *etcdbackup.RestoreMarker, nodes restoreNodes) {
	r := run.r
	job := &restoreJob{
		key:    types.NamespacedName{Namespace: run.op.Namespace, Name: run.op.Name},
		backup: types.NamespacedName{Namespace: run.op.Namespace, Name: run.op.Spec.EtcdRestore.BackupRef.Name},
		store:  run.store, prefix: run.prefix, passphrase: run.passphrase, scrub: run.scrub,
		target: r.restoreNode(run.cluster, nodes.target, run.bundle),
		marker: *m, now: time.Now, sent: map[string]time.Time{},
	}

	for _, n := range nodes.order {
		job.order = append(job.order, r.restoreNode(run.cluster, n, run.bundle))
	}

	// The controller's own node comes from the marker, not from where this process runs: a controller
	// restarted elsewhere still has to see it through.
	if i := slices.IndexFunc(nodes.all, func(c *ichorv1alpha1.TalosMachine) bool { return c.Name == m.Own && m.Own != "" }); i >= 0 {
		own := r.restoreNode(run.cluster, nodes.all[i], run.bundle)
		job.own = &own

		for _, c := range nodes.all {
			if c.Name != m.Own && nodeNameFor(m.Nodes, c) != "" {
				job.peers = append(job.peers, nodeNameFor(m.Nodes, c))
			}
		}
	}

	if r.launchTail != nil {
		r.launchTail(job)

		return
	}

	defaultRestoreTails.start(job.key, job.backup, func() { job.run(context.WithoutCancel(context.Background())) })
}

// ensureTail starts the job again after a restart of this process, from the marker. It does nothing
// when this process already runs it.
func (run *restoreRun) ensureTail(ctx context.Context, m *etcdbackup.RestoreMarker) error {
	if run.r.launchTail == nil && defaultRestoreTails.isRunning(types.NamespacedName{Namespace: run.op.Namespace, Name: run.op.Name}) {
		return nil
	}

	cps, err := run.r.restoreCandidates(ctx, run.cluster)
	if err != nil {
		return err
	}

	nodes := planRestoreNodes(cps, run.r.NodeName, m.Nodes)
	if m.Own != "" {
		// The controller's own node is the one the marker names, wherever this process runs now.
		nodes.order = slices.DeleteFunc(nodes.order, func(c *ichorv1alpha1.TalosMachine) bool { return c.Name == m.Own })
	}

	idx := slices.IndexFunc(nodes.all, func(c *ichorv1alpha1.TalosMachine) bool { return c.Name == m.Target })
	if idx < 0 {
		return fmt.Errorf("the target control plane %q is no longer an approved control plane", m.Target)
	}

	nodes.target = nodes.all[idx]

	run.launch(m, nodes)

	return nil
}

// advanceEtcdRestore drives a restore that has started. It takes no lock until the Kubernetes API is
// known to be back: before that the lock cannot even be read.
//
//nolint:gocyclo // the state machine, one switch
func (r *TalosOperationReconciler) advanceEtcdRestore(
	ctx context.Context, op *ichorv1alpha1.TalosOperation, cluster *ichorv1alpha1.TalosCluster,
) (ctrl.Result, error) {
	if op.Status.Phase == ichorv1alpha1.TalosOperationPhasePaused && op.Status.FailureReason != "" {
		// a failed step is never retried by itself, only by an explicit request
		if retried, err := r.consumeRetry(ctx, op); err != nil || !retried {
			return ctrl.Result{}, err
		}
	}

	if r.TalosClient == nil {
		return ctrl.Result{}, r.setPhase(ctx, op, ichorv1alpha1.TalosOperationPhasePaused,
			reasonOperationNoExecutor, "no Talos client is wired up for this controller")
	}

	run, err := r.newRestoreRun(ctx, op, cluster)
	if err != nil {
		return r.restoreWait(ctx, op, fmt.Sprintf("cannot read the restore configuration yet: %v", err)), nil
	}

	marker, err := etcdbackup.GetMarker(ctx, run.store, run.prefix, run.passphrase)
	if err != nil {
		return r.restoreWait(ctx, op, "waiting for the backup storage: "+run.clean(err)), nil
	}

	st := op.Status.EtcdRestore

	if marker == nil {
		if st.Step == ichorv1alpha1.EtcdRestoreStepReconciling {
			return run.finish(ctx, nil) // the marker was cleared; only the status write was missing
		}

		return run.failed(ctx, nil, errors.New("the restore marker is gone from the backup storage, so how far the restore got is unknown; "+
			"check each control plane's etcd before doing anything else"))
	}

	if !run.ours(marker) {
		return run.failed(ctx, marker, fmt.Errorf("the backup storage records a restore by operation %s/%s, not this one", marker.Namespace, marker.Operation))
	}

	// The status can be ahead of the marker (an operation recreated after the rollback starts at
	// Rejoining): the marker only moves forward.
	if etcdRestoreStepRank(st.Step) > etcdRestoreStepRank(marker.Step) {
		marker.Step = ichorv1alpha1.EtcdRestoreStepRejoining
		if err := etcdbackup.PutMarker(ctx, run.store, run.prefix, run.passphrase, marker); err != nil {
			return r.restoreWait(ctx, op, "waiting for the backup storage: "+run.clean(err)), nil
		}
	}

	if marker.Failure != "" {
		return run.failed(ctx, marker, errors.New(marker.Failure))
	}

	if time.Since(marker.StartedAt) > etcdRestoreOverallTimeout {
		return run.failed(ctx, marker, fmt.Errorf("the restore did not finish within %s", etcdRestoreOverallTimeout))
	}

	// A job that still has the controller's own node to wipe keeps running (or is started again after a
	// restart) past Rejoining; it needs only Talos.
	if marker.Own != "" && !marker.OwnDone && marker.Step == ichorv1alpha1.EtcdRestoreStepRejoining {
		if err := run.ensureTail(ctx, marker); err != nil {
			return r.restoreWait(ctx, op, err.Error()), nil
		}
	}

	if etcdRestoreStepRank(marker.Step) < etcdRestoreStepRank(ichorv1alpha1.EtcdRestoreStepRejoining) {
		if err := run.ensureTail(ctx, marker); err != nil {
			return r.restoreWait(ctx, op, err.Error()), nil
		}

		run.mirror(ctx, marker, marker.LastNote)

		return ctrl.Result{RequeueAfter: etcdRestoreWaitInterval}, nil
	}

	return run.rejoin(ctx, marker)
}

// restoreWait notes why a started restore is waiting and looks again soon. Writing the note is best
// effort: while the API is away it cannot be written.
func (r *TalosOperationReconciler) restoreWait(ctx context.Context, op *ichorv1alpha1.TalosOperation, why string) ctrl.Result {
	if op.Status.EtcdRestore != nil && op.Status.EtcdRestore.Message != why {
		op.Status.EtcdRestore.Message = why

		if err := r.Status().Update(ctx, op); err != nil {
			logf.FromContext(ctx).V(1).Info("restore note not recorded", "error", err.Error())
		}
	}

	return ctrl.Result{RequeueAfter: etcdRestoreWaitInterval}
}

// mirror copies the marker's step and the job's note into the operation's status, when the API
// accepts the write.
func (run *restoreRun) mirror(ctx context.Context, m *etcdbackup.RestoreMarker, message string) {
	st := run.op.Status.EtcdRestore
	if etcdRestoreStepRank(m.Step) <= etcdRestoreStepRank(st.Step) && (message == "" || message == st.Message) {
		return
	}

	if etcdRestoreStepRank(m.Step) > etcdRestoreStepRank(st.Step) {
		st.Step = m.Step
	}

	if message != "" {
		st.Message = message
	}

	st.Target = m.Target
	if err := run.r.Status().Update(ctx, run.op); err != nil {
		logf.FromContext(ctx).V(1).Info("restore step not mirrored to the status", "error", err.Error())
	}
}

// etcdRestoreFallback is what the operator is told when the restore stops: the offline recovery needs
// only the secrets bundle and the bucket, no Kubernetes and no controller.
const etcdRestoreFallback = "To finish by hand, without Kubernetes or this controller: `ichor restore --recover-cluster` " +
	"(it needs the cluster's secrets bundle and the backup bucket)."

// failed pauses the operation with the reason, in the status and (when there is a marker) in the marker
// so that a recreated operation shows it too. It is final: a failed restore step is not retried.
func (run *restoreRun) failed(ctx context.Context, m *etcdbackup.RestoreMarker, cause error) (ctrl.Result, error) {
	msg := run.clean(cause)

	if m != nil && run.ours(m) && !strings.Contains(msg, etcdRestoreFallback) {
		msg += " " + etcdRestoreFallback
	}

	if m != nil && m.Failure == "" {
		next := *m
		next.Failure = msg
		_ = etcdbackup.PutMarker(ctx, run.store, run.prefix, run.passphrase, &next)
	}

	return ctrl.Result{}, run.r.setPhase(ctx, run.op, ichorv1alpha1.TalosOperationPhasePaused, reasonOperationStepFailed, msg)
}

// rejoin is everything after etcd was recovered. The Kubernetes API is expected back, so the
// operation is looked up live: if it is gone, the rollback happened and it is recreated from the marker.
//
//nolint:gocyclo // the state machine, one list of waits
func (run *restoreRun) rejoin(ctx context.Context, m *etcdbackup.RestoreMarker) (ctrl.Result, error) {
	r, op, cluster := run.r, run.op, run.cluster
	reader := readerOrClient(r.APIReader, r.Client)

	var live ichorv1alpha1.TalosOperation

	switch err := reader.Get(ctx, client.ObjectKeyFromObject(op), &live); {
	case apierrors.IsNotFound(err):
		return ctrl.Result{}, r.recreateRestoreOperation(ctx, m)
	case err != nil:
		return r.restoreWait(ctx, op, "waiting for the Kubernetes API to come back: "+err.Error()), nil
	case live.UID != op.UID:
		return ctrl.Result{RequeueAfter: time.Second}, nil // the cache still holds the operation of before the rollback
	}

	// The API answers: the lock is a Kubernetes object again.
	lock, err := r.acquireDisruptiveLock(ctx, cluster, op)
	if err != nil {
		return r.lockUnavailable(ctx, op, err)
	}

	st := op.Status.EtcdRestore
	if etcdRestoreStepRank(st.Step) < etcdRestoreStepRank(ichorv1alpha1.EtcdRestoreStepRejoining) {
		st.Step, st.Message = ichorv1alpha1.EtcdRestoreStepRejoining, "etcd is recovered; waiting for the other control planes to rejoin it."
		op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress

		if err := r.Status().Update(ctx, op); err != nil {
			return ctrl.Result{}, err
		}
	}

	if st.Step == ichorv1alpha1.EtcdRestoreStepRejoining {
		if why, err := run.waitForMembers(ctx, m); err != nil {
			return ctrl.Result{}, err
		} else if why != "" {
			return r.restoreWait(ctx, op, why), nil
		}

		if err := run.apiReady(ctx); err != nil {
			return r.restoreWait(ctx, op, "waiting for the Kubernetes API: "+err.Error()), nil
		}

		why, err := run.refreshAgents(ctx, m)
		if err != nil {
			return ctrl.Result{}, err
		}

		if why != "" {
			return r.restoreWait(ctx, op, why), nil
		}

		if err := run.requestReconcile(ctx, m.AgentWarning); err != nil {
			return ctrl.Result{}, err
		}
	}

	run.warn = m.AgentWarning

	why, err := run.reconciled(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	if why != "" {
		return r.restoreWait(ctx, op, strings.TrimSpace(why+" "+run.warn)), nil
	}

	return run.finish(ctx, lock)
}

// waitForMembers is the Rejoining wait: every approved control plane is in the target's etcd. A control
// plane that has etcd data but is not a member is one the wipe did not reach (the controller's own node
// is left for this on purpose): it is wiped now. why is empty when everyone is in.
func (run *restoreRun) waitForMembers(ctx context.Context, m *etcdbackup.RestoreMarker) (why string, err error) {
	r := run.r

	cps, err := r.restoreCandidates(ctx, run.cluster)
	if err != nil {
		return "", err
	}

	target := slices.IndexFunc(cps, func(c *ichorv1alpha1.TalosMachine) bool { return c.Name == m.Target })
	if target < 0 {
		return "", fmt.Errorf("the target control plane %q is no longer an approved control plane", m.Target)
	}

	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	tc, err := r.TalosClient(dialCtx, run.cluster, cps[target], run.bundle)
	if err != nil {
		return fmt.Sprintf("dialing %s: %v", m.Target, run.clean(err)), nil
	}

	defer closeTalosClient(tc)

	members, err := tc.EtcdMemberList(dialCtx)
	if err != nil {
		return fmt.Sprintf("etcd on %s does not answer yet: %s", m.Target, run.clean(err)), nil
	}

	for _, cp := range cps {
		if cp.Name == m.Own {
			continue // the job wipes it, through Talos alone and after the others joined
		}

		run.wipeIfStale(ctx, cp, members)
	}

	var missing []string

	for _, cp := range cps {
		if name := nodeNameFor(m.Nodes, cp); name != "" && !etcdMemberPresent(members, name) {
			missing = append(missing, name)
		}
	}

	switch {
	case len(missing) > 0:
		return fmt.Sprintf("waiting for control plane(s) %s to rejoin etcd", strings.Join(missing, ", ")), nil
	case len(members) < len(cps):
		return fmt.Sprintf("etcd lists %d members for %d control planes: waiting for the rest to rejoin", len(members), len(cps)), nil
	}

	return "", nil
}

// wipeIfStale resets cp's etcd data when it has some although it is not a member of the recovered etcd:
// it still holds the old cluster's data, and would never join.
func (run *restoreRun) wipeIfStale(ctx context.Context, cp *ichorv1alpha1.TalosMachine, members []EtcdMember) {
	r := run.r
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)

	defer cancel()

	c, err := r.TalosClient(dialCtx, run.cluster, cp, run.bundle)
	if err != nil {
		return
	}

	defer closeTalosClient(c)

	name, err := resolveNodeName(dialCtx, r.Client, c, cp)
	if err != nil || etcdMemberPresent(members, name) {
		return
	}

	present, err := c.EtcdDataPresent(dialCtx)
	if err != nil || !present {
		return
	}

	// The time of the last reset is kept in the operation's status (a condition per machine), so that a
	// restarted controller does not ask a node that is already wiping once more.
	condType := staleWipeConditionPrefix + cp.Name
	if c := apimeta.FindStatusCondition(run.op.Status.Conditions, condType); c != nil && time.Since(c.LastTransitionTime.Time) < etcdRestoreResendAfter {
		return
	}

	apimeta.RemoveStatusCondition(&run.op.Status.Conditions, condType)
	apimeta.SetStatusCondition(&run.op.Status.Conditions, metav1.Condition{
		Type: condType, Status: metav1.ConditionTrue, Reason: "StaleEtcdDataWiped",
		Message: "etcd data that is not a member of the restored cluster was reset", ObservedGeneration: run.op.Generation,
	})

	// Recorded before the reset is sent; when it cannot be recorded, nothing is sent this time.
	if err := r.Status().Update(ctx, run.op); err != nil {
		logf.FromContext(ctx).Info("recording the wipe of a control plane failed", "machine", cp.Name, "error", err.Error())

		return
	}

	if err := c.ResetEtcdData(dialCtx); err != nil {
		logf.FromContext(ctx).Info("wiping a control plane that holds stale etcd data failed", "machine", cp.Name, "error", run.clean(err))
	}
}

// apiReady checks the Kubernetes API with a read, when a clientset is wired.
func (run *restoreRun) apiReady(ctx context.Context) error {
	if run.r.KubeClient == nil {
		return nil
	}

	_, err := run.r.KubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})

	return err
}

// requestReconcile asks every approved machine to reconcile again, with a token the machine controller
// echoes in its status once it has.
func (run *restoreRun) requestReconcile(ctx context.Context, warning string) error {
	r, op := run.r, run.op
	token := string(op.UID)

	machines, err := run.approved(ctx, deleteguard.MachineFilter{})
	if err != nil {
		return err
	}

	for i := range machines {
		m := &machines[i]
		if m.Annotations[ichorv1alpha1.EtcdRestoreReconcileAnnotation] == token {
			continue
		}

		patch := client.MergeFrom(m.DeepCopy())

		if m.Annotations == nil {
			m.Annotations = map[string]string{}
		}

		m.Annotations[ichorv1alpha1.EtcdRestoreReconcileAnnotation] = token

		if err := r.Patch(ctx, m, patch); err != nil {
			return fmt.Errorf("asking machine %q to reconcile: %w", m.Name, err)
		}
	}

	st := op.Status.EtcdRestore
	st.Step, st.ReconcileRequest = ichorv1alpha1.EtcdRestoreStepReconciling, token
	st.Message = strings.TrimSpace("the cluster is back; waiting for every machine to reconcile against the restored state. " + warning)

	return r.Status().Update(ctx, op)
}

// reconciled returns why the machines are not all reconciled and Ready yet, or "".
func (run *restoreRun) reconciled(ctx context.Context) (string, error) {
	token := run.op.Status.EtcdRestore.ReconcileRequest

	machines, err := run.approved(ctx, deleteguard.MachineFilter{})
	if err != nil {
		return "", err
	}

	var waiting []string

	for i := range machines {
		m := &machines[i]

		switch {
		case m.Status.ObservedReconcileRequest != token:
			waiting = append(waiting, m.Name+" (has not reconciled yet)")
		case !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionReady):
			waiting = append(waiting, fmt.Sprintf("%s (%s)", m.Name, notReadyReason(m)))
		}
	}

	if len(waiting) == 0 {
		return "", nil
	}

	slices.Sort(waiting)

	return "waiting for machines to be Ready after reconciling the restored state: " + strings.Join(waiting[:min(len(waiting), 3)], "; "), nil
}

func notReadyReason(m *ichorv1alpha1.TalosMachine) string {
	if c := apimeta.FindStatusCondition(m.Status.Conditions, conditionReady); c != nil {
		return c.Reason + ": " + c.Message
	}

	return "no Ready condition"
}

// finish ends the restore: the pinned copy and the marker go (the marker last, so that a failure to
// delete it leaves a restore that finishes again), then the lock is released.
func (run *restoreRun) finish(ctx context.Context, lock *clusterlock.Lock) (ctrl.Result, error) {
	if err := run.cleanup(ctx); err != nil {
		return ctrl.Result{}, err
	}

	if lock != nil {
		if err := lock.Release(ctx, controllerIdentity); err != nil {
			return ctrl.Result{}, err
		}
	}

	key := types.NamespacedName{Namespace: run.op.Namespace, Name: run.op.Name}
	defaultRestoreTails.forget(key)

	run.op.Status.EtcdRestore.Message = strings.TrimSpace("etcd is restored and every machine reconciled. " + run.warn)

	msg := "etcd is restored from the snapshot and every machine is Ready"
	if run.warn != "" {
		msg += "; warning: " + run.warn
	}

	return ctrl.Result{}, run.r.setPhase(ctx, run.op, ichorv1alpha1.TalosOperationPhaseCompleted, reasonEtcdRestoreDone, msg)
}

// cleanup removes the pinned snapshot copy and the marker.
func (run *restoreRun) cleanup(ctx context.Context) error {
	if err := run.store.Delete(ctx, etcdbackup.PinnedKey(run.prefix)); err != nil {
		return fmt.Errorf("removing the pinned snapshot: %s", run.clean(err))
	}

	if err := etcdbackup.DeleteMarker(ctx, run.store, run.prefix); err != nil {
		return fmt.Errorf("removing the restore marker: %s", run.clean(err))
	}

	return nil
}

// recreateRestoreOperation creates the TalosOperation for marker m, with the status that tells
// advanceEtcdRestore the cluster has been rolled back to the snapshot: from here only the rejoin and
// the reconcile are left.
func (r *TalosOperationReconciler) recreateRestoreOperation(ctx context.Context, m *etcdbackup.RestoreMarker) error {
	op := &ichorv1alpha1.TalosOperation{}
	op.Name, op.Namespace = m.Operation, m.Namespace
	op.Spec = ichorv1alpha1.TalosOperationSpec{
		ClusterRef: corev1.LocalObjectReference{Name: m.Cluster}, Kind: ichorv1alpha1.TalosOperationKindEtcdRestore,
		EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
			BackupRef: corev1.LocalObjectReference{Name: m.Backup}, Snapshot: m.Snapshot, Confirm: m.Cluster, Reason: m.Reason,
		},
	}

	if err := r.Create(ctx, op); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}

		// A first try created it and failed to write the status: finish that.
		if err := readerOrClient(r.APIReader, r.Client).Get(ctx, client.ObjectKeyFromObject(op), op); err != nil {
			return err
		}

		if op.Status.EtcdRestore != nil {
			return nil
		}
	}

	snap, started := metav1.NewTime(m.SnapshotTime), metav1.NewTime(m.StartedAt)
	op.Status.Phase = ichorv1alpha1.TalosOperationPhaseInProgress
	op.Status.EtcdRestore = &ichorv1alpha1.TalosOperationEtcdRestoreStatus{
		Step: ichorv1alpha1.EtcdRestoreStepRejoining, Target: m.Target, SnapshotTime: &snap, RequestedAt: &started,
		Message: "the cluster was rolled back to the snapshot and the operation recreated; waiting for the other control planes to rejoin etcd.",
	}

	if m.Failure != "" {
		op.Status.Phase, op.Status.FailureReason = ichorv1alpha1.TalosOperationPhasePaused, m.Failure
	}

	return r.Status().Update(ctx, op)
}

// resumeRolledBack is called when an operation this process knows to be mid-restore is gone from the
// API: the cluster was rolled back. It recreates it from the marker.
func (r *TalosOperationReconciler) resumeRolledBack(ctx context.Context, key types.NamespacedName) {
	backup, ok := defaultRestoreTails.backupOf(key)
	if !ok {
		return
	}

	if err := r.resumeBackup(ctx, backup); err != nil {
		logf.FromContext(ctx).Error(err, "resuming an etcd restore", "operation", key.String())
	}
}

// ResumeEtcdRestores is one pass over the markers, with the API up. A restore whose operation is gone
// (the rollback deleted it) is recreated from its marker. It reads one small object per
// TalosEtcdBackup. The error lists what could not be done this time; ResumeEtcdRestoresUntilDone
// repeats the pass.
func (r *TalosOperationReconciler) ResumeEtcdRestores(ctx context.Context) error {
	var backups ichorv1alpha1.TalosEtcdBackupList
	if err := readerOrClient(r.APIReader, r.Client).List(ctx, &backups); err != nil {
		return err
	}

	var errs []error

	for i := range backups.Items {
		key := client.ObjectKeyFromObject(&backups.Items[i])
		err := r.resumeBackup(ctx, key)

		switch {
		case err == nil:
		case errors.Is(err, errBackupUnusable):
			// Not something a retry fixes (a backup whose storage settings cannot be read): it must not
			// keep the others, or the manager's runner, waiting.
			logf.FromContext(ctx).Info("cannot look for an etcd restore marker", "backup", key.String(), "error", err.Error())
		default:
			errs = append(errs, fmt.Errorf("looking for an unfinished etcd restore of backup %s: %w", key.String(), err))
		}
	}

	return errors.Join(errs...)
}

// errBackupUnusable marks a backup whose destination settings cannot be loaded.
var errBackupUnusable = errors.New("the backup's storage settings cannot be loaded")

// resumeBackoffStart and resumeBackoffMax bound the pause between two passes of
// ResumeEtcdRestoresUntilDone. Variables so tests can shorten them.
var (
	resumeBackoffStart = time.Second
	resumeBackoffMax   = 30 * time.Second
)

// ResumeEtcdRestoresUntilDone repeats ResumeEtcdRestores, with a growing pause, until a pass has
// nothing left to do or ctx ends. It is what the manager runs. Creating the operation goes through the
// override admission webhook (fail-closed), which this same process serves and which is not in the
// Service until the Pod is Ready: the first passes after a start are expected to be refused, so a
// failure is retried, never given up on (the marker stays until the operation is back). It returns nil
// when ctx ends, so that it never takes the manager down.
func (r *TalosOperationReconciler) ResumeEtcdRestoresUntilDone(ctx context.Context) error {
	log := logf.FromContext(ctx)
	wait := resumeBackoffStart

	for {
		err := r.ResumeEtcdRestores(ctx)
		if err == nil {
			return nil
		}

		log.Info("an etcd restore is not resumed yet; trying again", "error", err.Error(), "in", wait.String())

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		wait = min(wait*2, resumeBackoffMax)
	}
}

// resumeBackup recreates the operation of backup's marker if the API no longer has it.
func (r *TalosOperationReconciler) resumeBackup(ctx context.Context, backup types.NamespacedName) error {
	reader := readerOrClient(r.APIReader, r.Client)

	var bk ichorv1alpha1.TalosEtcdBackup
	if err := reader.Get(ctx, backup, &bk); err != nil {
		return client.IgnoreNotFound(err)
	}

	var scrub scrubber

	var cluster ichorv1alpha1.TalosCluster
	if err := reader.Get(ctx, types.NamespacedName{Namespace: bk.Namespace, Name: bk.Spec.ClusterRef.Name}, &cluster); err != nil {
		return client.IgnoreNotFound(err)
	}

	_, passphrase, store, err := loadBackupStore(ctx, r.Client, r.ControllerNamespace, &cluster, bk.Spec.Destination.S3, r.NewStore, &scrub)
	if err != nil {
		return fmt.Errorf("%w: %s", errBackupUnusable, scrub.clean(err.Error()))
	}

	m, err := etcdbackup.GetMarker(ctx, store, bk.Spec.Destination.S3.Prefix, passphrase)
	if errors.Is(err, etcdbackup.ErrMarkerSignature) {
		// A marker this cluster did not sign is never acted on, and waiting does not change that: a
		// retry loop here would never end (and would hold up ResumeEtcdRestoresUntilDone for ever).
		return fmt.Errorf("%w: %s", errBackupUnusable, err.Error())
	}

	if err != nil {
		return errors.New(scrub.clean(err.Error()))
	}

	if m == nil || m.Namespace != bk.Namespace {
		return nil
	}

	var op ichorv1alpha1.TalosOperation

	switch err := reader.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Operation}, &op); {
	case err == nil:
		return nil // the operation is there: the normal reconcile carries on from the marker
	case !apierrors.IsNotFound(err):
		return err
	}

	// The operation is gone while its marker is not: the API was rolled back to the snapshot.
	if etcdRestoreStepRank(m.Step) < etcdRestoreStepRank(ichorv1alpha1.EtcdRestoreStepRejoining) {
		m.Step = ichorv1alpha1.EtcdRestoreStepRejoining
		if err := etcdbackup.PutMarker(ctx, store, bk.Spec.Destination.S3.Prefix, passphrase, m); err != nil {
			return errors.New(scrub.clean(err.Error()))
		}
	}

	return r.recreateRestoreOperation(ctx, m)
}

// etcdRestoreFinalizerStep keeps the operation from being deleted while the wipe or recovery runs, and
// clears the marker and the pinned snapshot when it is deleted otherwise. handled is true when the
// reconcile has nothing more to do.
func (r *TalosOperationReconciler) etcdRestoreFinalizerStep(ctx context.Context, op *ichorv1alpha1.TalosOperation) (handled bool, err error) {
	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindEtcdRestore {
		return false, nil
	}

	has := controllerutil.ContainsFinalizer(op, etcdRestoreFinalizer)

	if op.DeletionTimestamp.IsZero() {
		if !has && op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
			controllerutil.AddFinalizer(op, etcdRestoreFinalizer)

			return false, r.Update(ctx, op)
		}

		return false, nil
	}

	if !has {
		return true, nil
	}

	st := op.Status.EtcdRestore
	if st != nil && etcdRestoreStepRank(st.Step) > 0 && etcdRestoreStepRank(st.Step) < etcdRestoreStepRank(ichorv1alpha1.EtcdRestoreStepRejoining) &&
		op.Status.FailureReason == "" {
		return true, nil // wiping or recovering: the restore is half done and cannot be abandoned
	}

	// Only the operation's own marker is cleared; with no readable backup there is nothing to clear.
	if run, err := r.newRestoreRun(ctx, op, r.clusterOf(ctx, op)); err == nil {
		m, err := etcdbackup.GetMarker(ctx, run.store, run.prefix, run.passphrase)
		if err != nil {
			return true, err
		}

		if m != nil && run.ours(m) {
			if err := run.cleanup(ctx); err != nil {
				return true, err
			}
		}
	}

	defaultRestoreTails.forget(client.ObjectKeyFromObject(op))
	controllerutil.RemoveFinalizer(op, etcdRestoreFinalizer)

	return true, client.IgnoreNotFound(r.Update(ctx, op))
}

// clusterOf returns the operation's cluster, or an empty one when it cannot be read.
func (r *TalosOperationReconciler) clusterOf(ctx context.Context, op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosCluster {
	var c ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Spec.ClusterRef.Name}, &c); err != nil {
		return &ichorv1alpha1.TalosCluster{}
	}

	return &c
}

// agentsBound bounds the wait, after the kubelets of the nodes that were not wiped were restarted, for
// those nodes to be Ready again and for their kube-proxy pods to be recreated and Ready. Past it the
// restore carries on and says what did not converge. A variable so tests can shorten it.
var agentsBound = 10 * time.Minute

// unwipedNode is a node the restore did not wipe: a worker. It keeps running through the rollback.
type unwipedNode struct {
	machine *ichorv1alpha1.TalosMachine
	node    string
}

// unwipedNodes lists the approved workers with their Kubernetes node names.
func (run *restoreRun) unwipedNodes(ctx context.Context) ([]unwipedNode, error) {
	machines, err := run.approved(ctx, deleteguard.MachineFilter{})
	if err != nil {
		return nil, err
	}

	var workers []*ichorv1alpha1.TalosMachine

	for i := range machines {
		if machines[i].Spec.Role != roleControlPlane {
			workers = append(workers, &machines[i])
		}
	}

	names, _ := run.r.nodeNames(ctx, run.cluster, run.bundle, workers) // an unnamed one is reported below

	out := make([]unwipedNode, len(workers))
	for i, w := range workers {
		out[i] = unwipedNode{machine: w, node: names[w.Name]}
	}

	return out, nil
}

// refreshAgents makes the nodes that were not wiped follow the rewound API. They keep running
// through the rollback with what they cached from the cluster before it: on hardware the kubelet of such
// a worker kept failing "Error updating node status", and its kube-proxy pod stayed Terminating, so
// deleting the pod alone changed nothing. The order is therefore: restart the kubelet through Talos (the
// controller is the only writer to a node), wait until the node is Ready again with a heartbeat from after
// the restart, delete the kube-proxy pod that predates it, and wait until the replacement is Ready. It
// returns why it is not done, or "" when it is. After agentsBound it returns "" anyway and keeps what did
// not converge in the marker (AgentWarning) rather than claiming success.
//
// Cilium's pods are replaced the same way as kube-proxy's: Ichor does not operate
// the CNI, but a one-time pod restart after a restore, so that it drops what it cached from the old
// cluster, is in scope. It briefly drops that node's datapath state.
func (run *restoreRun) refreshAgents(ctx context.Context, m *etcdbackup.RestoreMarker) (string, error) {
	if run.r.KubeClient == nil || m.AgentWarning != "" {
		return "", nil
	}

	workers, err := run.unwipedNodes(ctx)
	if err != nil {
		return "", err
	}

	if len(workers) == 0 {
		return "", nil
	}

	log := logf.FromContext(ctx)

	if m.AgentsSince.IsZero() {
		// Recorded before the restart, so that a restart of this process does not restart the kubelets again.
		m.AgentsSince = time.Now().UTC()
		if err := etcdbackup.PutMarker(ctx, run.store, run.prefix, run.passphrase, m); err != nil {
			m.AgentsSince = time.Time{}

			return "waiting for the backup storage: " + run.clean(err), nil
		}

		for _, w := range workers {
			if err := run.restartKubelet(ctx, w.machine); err != nil {
				log.Info("restarting a kubelet failed", "machine", w.machine.Name, "error", run.clean(err))
			}
		}

		return "restarted the kubelet of the nodes that were not wiped; waiting for them to be Ready again.", nil
	}

	waiting := run.agentsWaiting(ctx, m.AgentsSince, workers)
	if len(waiting) == 0 {
		return "", nil
	}

	slices.Sort(waiting)

	why := strings.Join(waiting[:min(len(waiting), 3)], "; ")

	if time.Since(m.AgentsSince) > agentsBound {
		m.AgentWarning = fmt.Sprintf("after %s these nodes had not converged on the restored cluster: %s; check their kubelet and delete their kube-proxy pod by hand",
			agentsBound, strings.Join(waiting, "; "))

		if err := etcdbackup.PutMarker(ctx, run.store, run.prefix, run.passphrase, m); err != nil {
			m.AgentWarning = ""

			return "waiting for the backup storage: " + run.clean(err), nil
		}

		return "", nil
	}

	return "waiting for the nodes that were not wiped to follow the restored cluster: " + why, nil
}

// restartKubelet asks the node of w to restart its kubelet.
func (run *restoreRun) restartKubelet(ctx context.Context, w *ichorv1alpha1.TalosMachine) error {
	dialCtx, cancel := context.WithTimeout(ctx, etcdDialTimeout)
	defer cancel()

	c, err := run.r.TalosClient(dialCtx, run.cluster, w, run.bundle)
	if err != nil {
		return err
	}

	defer closeTalosClient(c)

	return c.ServiceRestart(dialCtx, serviceKubelet)
}

// agentsWaiting is what is still short on the nodes since the kubelets were restarted: each entry names
// a node. A stale kube-proxy pod (one created before the restart) is deleted on the way.
func (run *restoreRun) agentsWaiting(ctx context.Context, since time.Time, workers []unwipedNode) []string {
	kc := run.r.KubeClient

	// kube-proxy and the CNI (found the way the kube-proxy switch finds Cilium: by its DaemonSet name).
	// A DaemonSet that is not there is skipped: kube-proxy is replaced by the CNI, or another CNI runs.
	var agents []restoreAgent

	for _, name := range restoreAgentNames {
		ds, err := kc.AppsV1().DaemonSets(kubeSystemNamespace).Get(ctx, name, metav1.GetOptions{})

		switch {
		case err == nil && ds.Spec.Selector != nil:
			if s, serr := metav1.LabelSelectorAsSelector(ds.Spec.Selector); serr == nil && !s.Empty() {
				agents = append(agents, restoreAgent{name: name, ds: ds, selector: s})
			}
		case err != nil && !apierrors.IsNotFound(err):
			return []string{fmt.Sprintf("the %s DaemonSet cannot be read (%v)", name, err)}
		}
	}

	var waiting []string

	for _, w := range workers {
		if w.node == "" {
			waiting = append(waiting, w.machine.Name+" (its Kubernetes node name is unknown)")

			continue
		}

		if !run.nodeRefreshed(ctx, w.node, since) {
			waiting = append(waiting, w.node+" (not Ready since its kubelet was restarted)")

			continue
		}

		for _, a := range agents {
			waiting = append(waiting, run.agentPodWaiting(ctx, a, w.node, since)...)
		}
	}

	return waiting
}

// restoreAgentNames are the DaemonSets, in kube-system, whose pods on a node that was not wiped are
// replaced after a restore: kube-proxy and Cilium.
var restoreAgentNames = []string{kubeProxyName, ciliumName}

// restoreAgent is one such DaemonSet with the selector of its pods.
type restoreAgent struct {
	name     string
	ds       *appsv1.DaemonSet
	selector labels.Selector
}

// agentPodWaiting deletes the pod of a on node when it predates since, and says (as a one-entry list)
// when no Ready pod of it exists there yet.
func (run *restoreRun) agentPodWaiting(ctx context.Context, a restoreAgent, node string, since time.Time) []string {
	kc := run.r.KubeClient

	pods, err := kc.CoreV1().Pods(kubeSystemNamespace).List(ctx, metav1.ListOptions{LabelSelector: a.selector.String()})
	if err != nil {
		return []string{fmt.Sprintf("%s (listing its %s pod failed: %v)", node, a.name, err)}
	}

	fresh := false

	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName != node || !metav1.IsControlledBy(p, a.ds) && len(p.OwnerReferences) > 0 {
			continue
		}

		switch {
		case p.DeletionTimestamp != nil:
		case p.CreationTimestamp.Time.Before(since):
			if err := kc.CoreV1().Pods(kubeSystemNamespace).Delete(ctx, p.Name, metav1.DeleteOptions{}); client.IgnoreNotFound(err) != nil {
				logf.FromContext(ctx).Info("deleting a pod failed", "agent", a.name, "pod", p.Name, "node", node, "error", err.Error())
			}
		case podReady(p):
			fresh = true
		}
	}

	if !fresh {
		return []string{fmt.Sprintf("%s (no Ready %s pod yet)", node, a.name)}
	}

	return nil
}

// nodeRefreshed reports whether the Node is Ready with a heartbeat from after since: its Ready condition
// in the rewound API can be the stale one from the snapshot.
func (run *restoreRun) nodeRefreshed(ctx context.Context, name string, since time.Time) bool {
	kc := run.r.KubeClient

	node, err := kc.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false
	}

	for _, c := range node.Status.Conditions {
		if c.Type != corev1.NodeReady {
			continue
		}

		if c.Status != corev1.ConditionTrue {
			return false
		}

		if c.LastHeartbeatTime.After(since) {
			return true
		}
	}

	// Ready, but with a heartbeat that may predate the restart: the node lease is renewed more often.
	lease, err := kc.CoordinationV1().Leases(corev1.NamespaceNodeLease).Get(ctx, name, metav1.GetOptions{})

	return err == nil && lease.Spec.RenewTime != nil && lease.Spec.RenewTime.After(since)
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}

	return false
}
