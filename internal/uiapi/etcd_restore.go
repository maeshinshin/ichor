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

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// Etcd restore: one TalosOperation of kind etcd-restore rewinds the whole cluster to a
// stored snapshot. This file only describes and writes; the controller wipes and recovers etcd.

// etcdRestorePlan is what planEtcdRestore learned, kept for CreateOperation.
type etcdRestorePlan struct {
	backup   *ichorv1alpha1.TalosEtcdBackup
	snapshot string
	confirm  string
	bg       *pb.Override
}

//nolint:gocyclo // one list of independent refusals, then the plain-English plan
func (s *Server) planEtcdRestore(ctx context.Context, clusterRef *pb.ObjectRef, req *pb.EtcdRestoreRequest) (*opPlan, error) {
	if req == nil || req.GetBackup().GetName() == "" || req.GetSnapshot() == "" {
		return nil, invalid("an etcd restore needs the backup and the snapshot")
	}

	if clusterRef.GetName() == "" {
		return nil, invalid("an etcd restore needs the cluster")
	}

	cluster, err := s.loadCluster(ctx, namespacedName(clusterRef))
	if err != nil {
		return nil, err
	}

	var bk ichorv1alpha1.TalosEtcdBackup

	bkey := namespacedName(req.GetBackup())
	if err := s.Get(ctx, bkey, &bk); err != nil {
		return nil, mapGetError(fmt.Sprintf("etcd backup %q", bkey), err)
	}

	p := &opPlan{
		kind: ichorv1alpha1.TalosOperationKindEtcdRestore, cluster: cluster,
		restore: &etcdRestorePlan{backup: &bk, snapshot: req.GetSnapshot(), confirm: req.GetConfirm(), bg: req.GetOverride()},
		resp: &pb.PlanOperationResponse{
			MaxUnavailable: 1, Effect: pb.OperationEffect_OPERATION_EFFECT_REBOOT, KubernetesApiUnavailable: true,
			ConfirmName: cluster.Name, OverrideRequired: true,
		},
	}

	var cps []string

	var machines ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, mapListError("machines", err)
	}

	for i := range machines.Items {
		m := &machines.Items[i]
		if m.Spec.ClusterRef.Name == cluster.Name && m.Spec.IsApproved() && m.Spec.Role == opRoleControlPlane && m.DeletionTimestamp.IsZero() {
			cps = append(cps, m.Name)
			p.resp.Machines = append(p.resp.Machines, &pb.PlannedMachine{Name: m.Name, Role: m.Spec.Role, Step: 1})
		}
	}

	slices.Sort(cps)

	own := s.controllerMachine(ctx, machines.Items, cluster.Name, opRoleControlPlane)

	var taken time.Time

	for _, snap := range bk.Status.Snapshots {
		if snap.Name == req.GetSnapshot() {
			taken = snap.Time.Time
		}
	}

	if !taken.IsZero() {
		p.resp.SnapshotTime = timestamppb.New(taken)
	}

	p.resp.Steps = []string{
		"Check the snapshot: it is decrypted and its checksum verified, and a copy is kept in the backup storage so that retention cannot remove it.",
		"Take the cluster lock and write a marker next to the backups. It is how the restore carries on after the Kubernetes API is gone.",
		wipeStep(cps, own),
		"Upload the snapshot to one control plane and start etcd from it.",
		"The other control planes rejoin etcd by themselves. Wait until all are members and the Kubernetes API answers.",
		"Restart kube-proxy on the nodes that were not wiped, ask every machine to reconcile again and wait until each is Ready.",
	}
	p.resp.Warning = fmt.Sprintf("This rewinds the whole cluster to the state of %s. Everything written since is lost: "+
		"every Kubernetes object, including Ichor's own resources and Secrets, and every change to machines or this cluster. "+
		"The Kubernetes API is unavailable from the first wipe until etcd is back, which takes several minutes; "+
		"running pods keep running but nothing can be changed. Afterwards every machine reconciles against the restored "+
		"state, which may differ from what the nodes run, so check each machine. If Ichor's controller runs on the only "+
		"control plane it stops with it and `ichor restore --recover-cluster` finishes the restore. That command is also the way out "+
		"if the restore stops with an error.", whenText(taken))

	p.resp.Blocked, _ = s.etcdRestoreBlocked(ctx, p, cluster, &bk, taken, len(cps))

	if p.resp.Blocked == "" {
		blocked, err := s.unfinishedOperation(ctx, cluster)
		if err != nil {
			return nil, err
		}

		p.resp.Blocked = blocked
	}

	return p, nil
}

// wipeStep is the plan's wipe step. The control plane the controller runs on is wiped last, once etcd is
// back and the others have rejoined, by the controller itself, which stops with it.
func wipeStep(cps []string, own string) string {
	const tail = " Their EPHEMERAL partitions are reset and the nodes reboot. STATE stays, so each keeps its configuration."

	if own == "" || len(cps) < 2 {
		return fmt.Sprintf("Wipe etcd data on every control plane (%s).", strings.Join(cps, ", ")) + tail
	}

	rest := make([]string, 0, len(cps))

	for _, c := range cps {
		if c != own {
			rest = append(rest, c)
		}
	}

	return fmt.Sprintf("Wipe etcd data on %s, then %s (where this controller runs) once the others have rejoined.", strings.Join(rest, ", "), own) + tail
}

// controllerMachine is the machine of role (any role when "") the Ichor controller pod runs on, "" when it
// cannot be told (outside the cluster, or the pod or Node cannot be read): the plan then names nobody.
func (s *Server) controllerMachine(ctx context.Context, machines []ichorv1alpha1.TalosMachine, cluster, role string) string {
	// The namespace Ichor runs in, then the cluster's own, then (the in-process server runs on the caller's
	// credentials and a CLI process has no POD_NAMESPACE) every namespace: the first that has the pod wins.
	for _, ns := range s.controllerNamespaces(machines) {
		var pods corev1.PodList
		if err := s.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{"control-plane": "controller-manager"}); err != nil {
			continue
		}

		for i := range pods.Items {
			node := pods.Items[i].Spec.NodeName
			if node == "" {
				continue
			}

			for j := range machines {
				m := &machines[j]
				if m.Spec.ClusterRef.Name != cluster || (role != "" && m.Spec.Role != role) || !m.Spec.IsApproved() {
					continue
				}

				if n, err := s.findKubernetesNode(ctx, m); err == nil && n != nil && n.Name == node {
					return m.Name
				}
			}
		}
	}

	return ""
}

// controllerNamespaces are the namespaces controllerMachine looks for the controller pod in, in order;
// corev1.NamespaceAll (every namespace) comes last, and only for the in-process server.
func (s *Server) controllerNamespaces(machines []ichorv1alpha1.TalosMachine) []string {
	out := []string{s.ControllerNamespace}
	if len(machines) > 0 {
		out = append(out, machines[0].Namespace)
	}

	out = slices.DeleteFunc(slices.Compact(out), func(ns string) bool { return ns == "" })
	if s.CallerCredentials {
		out = append(out, corev1.NamespaceAll)
	}

	return out
}

func whenText(t time.Time) string {
	if t.IsZero() {
		return "the chosen snapshot"
	}

	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// etcdRestoreBlocked is the first reason the restore cannot start, or "".
func (s *Server) etcdRestoreBlocked(
	ctx context.Context, p *opPlan, cluster *ichorv1alpha1.TalosCluster, bk *ichorv1alpha1.TalosEtcdBackup, taken time.Time, controlPlanes int,
) (string, error) {
	switch {
	case bk.Spec.ClusterRef.Name != cluster.Name:
		return fmt.Sprintf("backup %q backs up cluster %q, not %q", bk.Name, bk.Spec.ClusterRef.Name, cluster.Name), nil
	case controlPlanes == 0:
		return "the cluster has no approved control plane", nil
	case etcdbackup.InClusterReason(bk.Spec.Destination.S3.Endpoint, cluster) != "":
		// Absolute: override does not override it, the restore marker could never be written.
		return etcdbackup.InClusterMessage(etcdbackup.InClusterReason(bk.Spec.Destination.S3.Endpoint, cluster)), nil
	case taken.IsZero():
		return fmt.Sprintf("snapshot %q is not among the snapshots backup %q last listed (it may have been pruned)", p.restore.snapshot, bk.Name), nil
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := s.List(ctx, &ops, client.InNamespace(cluster.Namespace)); err != nil {
		return "", mapListError("operations", err)
	}

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.Kind == ichorv1alpha1.TalosOperationKindCARotation && o.Spec.ClusterRef.Name == cluster.Name && o.CreationTimestamp.After(taken) {
			return fmt.Sprintf("operation %q rotated the cluster's CAs after this snapshot was taken: the restored secrets bundle would "+
				"not match the nodes' certificates, so choose a snapshot taken after the rotation", o.Name), nil
		}

		if o.Spec.ClusterRef.Name == cluster.Name && ichorv1alpha1.KeyRotationOutdatesSnapshot(o, taken) {
			return fmt.Sprintf("operation %q rotated the cluster's service-account or secretbox key and had not finished when this snapshot "+
				"was taken: the Secrets in it, the secrets bundle among them, are under keys the control planes no longer use, so choose a "+
				"snapshot taken after the rotation finished", o.Name), nil
		}
	}

	return "", nil
}

// createEtcdRestore checks what the plan could not and creates the TalosOperation.
func (s *Server) createEtcdRestore(ctx context.Context, p *opPlan) (*pb.CreateOperationResponse, error) {
	rp, cluster := p.restore, p.cluster

	if err := s.authorizeStart(ctx, &ichorv1alpha1.TalosOperationSpec{Kind: p.kind}, cluster.Namespace); err != nil {
		return nil, err
	}

	if rp.confirm != cluster.Name {
		return nil, invalid("type the cluster name %q to confirm the restore", cluster.Name)
	}

	// A restore is always an override; the reason is optional, and the spec's reason (required by the CRD)
	// then says that none was given.
	if rp.bg == nil {
		return nil, invalid("an etcd restore needs the override: it is checked against the override permission on the cluster")
	}

	reason := overrideReason(rp.bg.GetReason())
	if err := s.authorizeOverride(ctx, cluster); err != nil {
		return nil, err
	}

	op := s.newOperation(ctx, cluster, ichorv1alpha1.TalosOperationSpec{
		Kind: p.kind,
		EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
			BackupRef: corev1.LocalObjectReference{Name: rp.backup.Name}, Snapshot: rp.snapshot, Confirm: cluster.Name, Reason: reason,
		},
	})

	if err := s.createOperation(ctx, cluster, op); err != nil {
		return nil, err
	}

	audit := s.opAudit(ctx, "create-override", objectRef(op.Namespace, op.Name))
	audit.Reason = reason

	msg := fmt.Sprintf("etcd restore of %s from snapshot %s requested by %s under override: %s", cluster.Name, rp.snapshot, audit.Actor, reason)
	if err := s.emitWarningEvent(ctx, cluster, kindTalosCluster, "Override", msg); err != nil {
		audit.Reason += " (the Kubernetes Event could not be recorded)"
	}

	return &pb.CreateOperationResponse{Operation: operationToPB(op), Audit: audit}, nil
}

// ListEtcdBackups lists the backups of a cluster with the snapshots each last listed. It reads the
// backups as the caller; the storage itself is not touched.
func (s *Server) ListEtcdBackups(
	ctx context.Context, req *connect.Request[pb.ListEtcdBackupsRequest],
) (*connect.Response[pb.ListEtcdBackupsResponse], error) {
	ref := req.Msg.GetCluster()
	if ref.GetName() == "" {
		return nil, invalid("the cluster is required")
	}

	var list ichorv1alpha1.TalosEtcdBackupList
	if err := s.List(ctx, &list, client.InNamespace(ref.GetNamespace())); err != nil {
		return nil, mapListError("etcd backups", err)
	}

	out := &pb.ListEtcdBackupsResponse{}

	for i := range list.Items {
		bk := &list.Items[i]
		if bk.Spec.ClusterRef.Name != ref.GetName() {
			continue
		}

		out.Backups = append(out.Backups, etcdBackupToPB(bk))
	}

	slices.SortFunc(out.Backups, func(a, b *pb.EtcdBackup) int { return strings.Compare(a.GetRef().GetName(), b.GetRef().GetName()) })

	return connect.NewResponse(out), nil
}

// etcdBackupToPB is the page's view of a backup: its destination without the credentials, retention,
// and what the controller last recorded.
func etcdBackupToPB(bk *ichorv1alpha1.TalosEtcdBackup) *pb.EtcdBackup {
	s3 := bk.Spec.Destination.S3
	ready := apimeta.FindStatusCondition(bk.Status.Conditions, ichorv1alpha1.TalosEtcdBackupConditionReady)
	eb := &pb.EtcdBackup{
		Ref: objectRef(bk.Namespace, bk.Name), Schedule: bk.Spec.Schedule,
		Ready:       ready != nil && ready.Status == metav1.ConditionTrue,
		Destination: &pb.EtcdBackupDestination{Endpoint: s3.Endpoint, Region: s3.Region, Bucket: s3.Bucket, Prefix: s3.Prefix},
		KeepLast:    bk.Spec.Retention.KeepLast, Suspended: bk.Spec.Suspend,
	}

	if ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == ichorv1alpha1.TalosEtcdBackupReasonFailed {
		eb.LastError = ready.Message
	}

	if t := bk.Status.LastSuccessTime; t != nil {
		eb.LastSuccessTime = timestamppb.New(t.Time)
	}

	if t := bk.Status.LastManualBackup; t != nil {
		eb.LastManualBackup = timestamppb.New(t.Time)
	}

	if at, err := time.Parse(time.RFC3339, bk.Annotations[ichorv1alpha1.BackupNowAnnotation]); err == nil {
		last := bk.Status.LastManualBackup
		eb.ManualBackupPending = last == nil || at.After(last.Time)
	}

	for _, snap := range bk.Status.Snapshots {
		eb.Snapshots = append(eb.Snapshots, &pb.EtcdSnapshot{Name: snap.Name, Time: timestamppb.New(snap.Time.Time), Size: snap.Size})
	}

	return eb
}

// RequestEtcdBackup sets the backup-now annotation as the caller; the controller takes the snapshot. The time is
// never earlier than status.lastManualBackup plus a second, so a second
// request is newer than the one the controller already acted on, and a request that is still waiting
// is refused instead of stacked.
func (s *Server) RequestEtcdBackup(
	ctx context.Context, req *connect.Request[pb.RequestEtcdBackupRequest],
) (*connect.Response[pb.RequestEtcdBackupResponse], error) {
	ref := req.Msg.GetBackup()
	if ref.GetName() == "" {
		return nil, invalid("the backup is required")
	}

	var bk ichorv1alpha1.TalosEtcdBackup
	if err := s.Get(ctx, namespacedName(ref), &bk); err != nil {
		return nil, mapOperationWriteError("getting etcd backup", namespacedName(ref), err)
	}

	if bk.Spec.Suspend {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the backup is suspended (spec.suspend): no snapshot is taken until it is resumed"))
	}

	if etcdBackupToPB(&bk).GetManualBackupPending() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("an on-demand backup was already requested and has not started yet"))
	}

	at := s.now().UTC().Truncate(time.Second)
	if last := bk.Status.LastManualBackup; last != nil && !at.After(last.Time) {
		at = last.Add(time.Second)
	}

	metav1.SetMetaDataAnnotation(&bk.ObjectMeta, ichorv1alpha1.BackupNowAnnotation, at.Format(time.RFC3339))

	if err := s.Update(ctx, &bk, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("updating etcd backup", namespacedName(ref), err)
	}

	return connect.NewResponse(&pb.RequestEtcdBackupResponse{Backup: etcdBackupToPB(&bk), Audit: s.opAudit(ctx, "backup-now", ref)}), nil
}

// etcdRestoreToPB fills the restore fields of an Operation.
func etcdRestoreToPB(out *pb.Operation, op *ichorv1alpha1.TalosOperation) {
	spec := op.Spec.EtcdRestore
	if spec == nil {
		return
	}

	out.EtcdRestoreBackup = objectRef(op.Namespace, spec.BackupRef.Name)
	out.EtcdRestoreSnapshot = spec.Snapshot

	if st := op.Status.EtcdRestore; st != nil {
		out.EtcdRestoreStep, out.EtcdRestoreMessage = st.Step, st.Message

		if st.SnapshotTime != nil {
			out.EtcdRestoreSnapshotTime = timestamppb.New(st.SnapshotTime.Time)
		}
	}
}

// etcdRestoreHoldsPause reports that pausing op would be refused: once the wipe started the restore
// is half done, and the controller does not stop in the middle of it.
func etcdRestoreHoldsPause(op *ichorv1alpha1.TalosOperation) error {
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindEtcdRestore && op.Status.EtcdRestore != nil && op.Status.EtcdRestore.Step != "" &&
		op.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
		return connect.NewError(connect.CodeFailedPrecondition,
			errors.New("an etcd restore cannot be paused once it started: etcd is wiped or being recovered, and stopping halfway leaves no cluster"))
	}

	return nil
}
