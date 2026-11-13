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
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// Defaults of MachineReclaimReconciler's timings.
const (
	reclaimKind = "MachineReclaim"

	reclaimDefaultTTL         = 15 * time.Minute
	reclaimRetention          = 7 * 24 * time.Hour
	reclaimMaintenanceWait    = 10 * time.Minute
	reclaimWaitWindow         = 20 * time.Second
	reclaimWaitInterval       = 5 * time.Second
	reclaimRequeueWhileWaitng = 10 * time.Second
	reclaimReconcileTimeout   = 2 * time.Minute
	// ReclaimMaxCredentialAge is the hard maximum TTL: the sweeper deletes every credential Secret older.
	ReclaimMaxCredentialAge = 60 * time.Minute
)

// MachineReclaimReconciler runs "Reset and add": it inspects a
// machine of another Talos cluster with a user-supplied credential, waits for the typed UUID,
// sends the reset once and waits for maintenance mode. It never writes spec, and it does not
// register the machine: the UI API adds the address to the target cluster's discovery on the
// user's behalf when it creates the request (spec.clusterRef).
type MachineReclaimReconciler struct {
	client.Client

	// ControllerNamespace is where requests and credentials live (ichor-system). Empty means any.
	ControllerNamespace string
	Recorder            record.EventRecorder

	// Dial and Probe default to reclaim.Dial and reclaim.MaintenanceUUID.
	Dial  func(ctx context.Context, cred reclaim.Credential, address, contextName string) (reclaim.Node, error)
	Probe func(ctx context.Context, address string) (string, error)

	// Now, WaitWindow, WaitInterval and MaintenanceWait default to the constants above.
	Now             func() time.Time
	WaitWindow      time.Duration
	WaitInterval    time.Duration
	MaintenanceWait time.Duration
	// ReconcileTimeout bounds one reconcile (default reclaimReconcileTimeout), so a node call that
	// hangs cannot hold a worker for minutes while other requests wait.
	ReconcileTimeout time.Duration
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=machinereclaims,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=machinereclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch;update;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;delete

// Reconcile advances one MachineReclaim. A panic is turned into a plain error: its value could
// quote what the code was holding, and nothing about a credential may reach a log.
func (r *MachineReclaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			res, err = ctrl.Result{}, errors.New("internal error while reconciling a MachineReclaim (details withheld)")
		}
	}()

	ctx, stop := context.WithTimeout(ctx, cmp.Or(r.ReconcileTimeout, reclaimReconcileTimeout))
	defer stop()

	var mr ichorv1alpha1.MachineReclaim
	if err := r.Get(ctx, req.NamespacedName, &mr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.ControllerNamespace != "" && mr.Namespace != r.ControllerNamespace {
		return ctrl.Result{}, nil
	}

	now := nowFrom(r.Now)

	if mr.Status.ExpiresAt == nil {
		ttl := reclaimDefaultTTL
		if mr.Spec.TTL != nil && mr.Spec.TTL.Duration > 0 {
			ttl = min(mr.Spec.TTL.Duration, ReclaimMaxCredentialAge)
		}

		mr.Status.ExpiresAt = &metav1.Time{Time: mr.CreationTimestamp.Add(ttl)}
		mr.Status.Audit.RequestedBy = mr.Annotations[ichorv1alpha1.ReclaimRequestedByAnnotation]
		mr.Status.Phase = ichorv1alpha1.MachineReclaimInspecting

		if err := r.Status().Update(ctx, &mr); err != nil {
			return ctrl.Result{}, err
		}

		r.audit(&mr, corev1.EventTypeNormal, "ReclaimRequested",
			fmt.Sprintf("reclaim of %s requested by %s", mr.Spec.Address, cmp.Or(mr.Status.Audit.RequestedBy, "(unknown)")))
	}

	logf.FromContext(ctx).Info("reconciling a reclaim", "phase", mr.Status.Phase)

	switch mr.Status.Phase {
	case ichorv1alpha1.MachineReclaimDone, ichorv1alpha1.MachineReclaimFailed, ichorv1alpha1.MachineReclaimExpired:
		return r.retain(ctx, &mr, now)
	case ichorv1alpha1.MachineReclaimResetting:
		// The controller stopped between recording the reset and learning its outcome. The reset is
		// never repeated; whether it took effect shows in maintenance mode.
		mr.Status.Phase = ichorv1alpha1.MachineReclaimWaitingForMaintenance

		if err := r.Status().Update(ctx, &mr); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Second}, nil
	case ichorv1alpha1.MachineReclaimWaitingForMaintenance:
		return r.waitForMaintenance(ctx, &mr, now)
	}

	if !now.Before(mr.Status.ExpiresAt.Time) {
		return r.expire(ctx, &mr)
	}

	return r.attend(ctx, &mr, now)
}

// attend inspects the node, and when the user has typed the UUID, sends the reset.
func (r *MachineReclaimReconciler) attend(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, now time.Time) (ctrl.Result, error) {
	cred, err := r.credential(ctx, mr)
	if errors.Is(err, errSecretPending) {
		// The UI API writes the request first and the Secret a moment later. If it never appears,
		// the request expires at its TTL.
		return ctrl.Result{RequeueAfter: min(2*time.Second, mr.Status.ExpiresAt.Sub(now))}, nil
	}

	if err != nil {
		return r.fail(ctx, mr, err.Error())
	}

	dial := r.Dial
	if dial == nil {
		dial = reclaim.Dial
	}

	node, err := dial(ctx, cred, mr.Spec.Address, mr.Spec.ContextName)
	if err != nil {
		return r.fail(ctx, mr, safeError(mr.Spec.Address, "connecting", err))
	}
	defer node.Close() //nolint:errcheck

	known, err := reclaim.LoadKnown(ctx, r.Client, r.ControllerNamespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cannot tell which nodes Ichor manages: %w", err)
	}

	opt := reclaim.Options{Force: mr.Spec.Force, ForceUngraceful: mr.Spec.ForceUngraceful}

	if mr.Status.Phase == ichorv1alpha1.MachineReclaimAwaitingConfirmation && mr.Spec.ConfirmUUID != "" {
		return r.reset(ctx, mr, node, known, opt)
	}

	in, err := node.Inspect(ctx)
	if err != nil {
		return r.fail(ctx, mr, safeError(mr.Spec.Address, "inspecting", err))
	}

	plan := reclaim.Assess(in, known, opt)
	first := mr.Status.Phase != ichorv1alpha1.MachineReclaimAwaitingConfirmation

	mr.Status.Inspection = inspectionStatus(in, plan)
	mr.Status.Phase = ichorv1alpha1.MachineReclaimAwaitingConfirmation

	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}

	if first {
		r.audit(mr, corev1.EventTypeNormal, "ReclaimInspected",
			fmt.Sprintf("UUID %s, cluster %q (%s)", in.UUID, in.ClusterName, in.ClusterID))
	}

	return ctrl.Result{RequeueAfter: mr.Status.ExpiresAt.Sub(now)}, nil
}

// resetRecorder records the reset in status before it is sent, so that a second reset from the
// same request is impossible whatever happens next.
type resetRecorder struct {
	reclaim.Node
	before func() error
	sent   bool
}

func (n *resetRecorder) Reset(ctx context.Context, graceful bool) error {
	if err := n.before(); err != nil {
		return err
	}

	n.sent = true

	return n.Node.Reset(ctx, graceful)
}

// reset sends the one reset this request may send.
func (r *MachineReclaimReconciler) reset(
	ctx context.Context, mr *ichorv1alpha1.MachineReclaim, node reclaim.Node, known reclaim.Known, opt reclaim.Options,
) (ctrl.Result, error) {
	if mr.Status.Audit.ResetSentAt != nil {
		return r.fail(ctx, mr, "this request already sent its reset; a new request needs a new paste of the credential")
	}

	confirmedBy := mr.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation]

	r.audit(mr, corev1.EventTypeNormal, "ReclaimConfirmed",
		fmt.Sprintf("%s confirmed UUID %s", cmp.Or(confirmedBy, "(unknown)"), mr.Spec.ConfirmUUID))

	rec := &resetRecorder{Node: node}
	rec.before = func() error {
		t := metav1.NewTime(nowFrom(r.Now))
		mr.Status.Phase = ichorv1alpha1.MachineReclaimResetting
		mr.Status.Audit.ConfirmedBy = confirmedBy
		mr.Status.Audit.ConfirmedAt = &t
		mr.Status.Audit.ResetSentAt = &t

		return r.Status().Update(ctx, mr)
	}

	in, err := reclaim.Execute(ctx, rec, known, opt, reclaim.Confirmation{
		UUID: mr.Spec.ConfirmUUID, ClusterName: mr.Spec.ConfirmClusterName,
	})
	if err != nil {
		msg := safeError(mr.Spec.Address, "the reset", err)
		if rec.sent {
			msg += "; the reset may or may not have been applied, check the node"
		}

		return r.fail(ctx, mr, msg)
	}

	r.audit(mr, corev1.EventTypeNormal, "ReclaimReset",
		fmt.Sprintf("reset sent to %s (STATE and EPHEMERAL, reboot), graceful: %t", mr.Spec.Address,
			reclaim.Assess(in, known, opt).Graceful))

	// The credential has done its one job.
	r.deleteCredential(ctx, mr)

	mr.Status.Phase = ichorv1alpha1.MachineReclaimWaitingForMaintenance
	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Second}, nil
}

func (r *MachineReclaimReconciler) waitForMaintenance(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, now time.Time) (ctrl.Result, error) {
	host := reclaim.HostOnly(mr.Spec.Address)
	uuid := ""

	if mr.Status.Inspection != nil {
		uuid = mr.Status.Inspection.UUID
	}

	probe := r.Probe
	if probe == nil {
		probe = reclaim.MaintenanceUUID
	}

	sent := mr.CreationTimestamp.Time
	if mr.Status.Audit.ResetSentAt != nil {
		sent = mr.Status.Audit.ResetSentAt.Time
	}

	// The deadline counts from the reset, not from the TTL: once the reset is sent the request
	// lives until the machine is back or this deadline passes.
	deadline := sent.Add(cmp.Or(r.MaintenanceWait, reclaimMaintenanceWait))

	failed := func() (ctrl.Result, error) {
		return r.fail(ctx, mr, fmt.Sprintf("%s did not return to maintenance mode with UUID %s. It may have taken a new "+
			"address from DHCP: scan for the node and add its address to a cluster's spec.discovery.addresses", host, uuid))
	}

	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return failed()
	}

	// Never probe past the deadline.
	wctx, cancel := context.WithTimeout(ctx, min(cmp.Or(r.WaitWindow, reclaimWaitWindow), remaining))
	defer cancel()

	err := reclaim.WaitMaintenance(wctx, host, uuid, cmp.Or(r.WaitInterval, reclaimWaitInterval), probe)

	switch {
	case err == nil:
		return r.done(ctx, mr, host)
	case errors.Is(err, reclaim.ErrOtherMachine):
		return r.fail(ctx, mr, fmt.Sprintf("%s now answers in maintenance mode as a different node; "+
			"find the reclaimed one (UUID %s) by scanning", host, uuid))
	}

	// The probes took time: measure again, and come back exactly at the deadline at the latest.
	remaining = deadline.Sub(nowFrom(r.Now))
	if remaining <= 0 {
		return failed()
	}

	return ctrl.Result{RequeueAfter: min(reclaimRequeueWhileWaitng, remaining)}, nil
}

// done reports the maintenance address. Registration is the user's write (see the type's
// comment); when spec.clusterRef is set the result says whether that write has happened.
func (r *MachineReclaimReconciler) done(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, host string) (ctrl.Result, error) {
	result := fmt.Sprintf("%s is in maintenance mode", host)

	if ref := mr.Spec.ClusterRef; ref != nil && ref.Name != "" {
		var cluster ichorv1alpha1.TalosCluster

		err := r.Get(ctx, client.ObjectKey{Namespace: mr.Namespace, Name: ref.Name}, &cluster)

		if err == nil && cluster.Spec.Discovery != nil && slices.Contains(cluster.Spec.Discovery.Addresses, host) {
			result += fmt.Sprintf("; cluster %s discovers it and will show it as a pending node", ref.Name)
		} else {
			result += fmt.Sprintf("; add %s to cluster %s's spec.discovery.addresses to register it", host, ref.Name)
		}
	} else {
		result += "; add its address to a cluster's spec.discovery.addresses to register it"
	}

	result += r.deleteOrphanNode(ctx, mr)

	mr.Status.Phase = ichorv1alpha1.MachineReclaimDone
	mr.Status.Result = result

	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}

	r.audit(mr, corev1.EventTypeNormal, "ReclaimCompleted", result)

	return ctrl.Result{RequeueAfter: time.Second}, nil
}

// deleteOrphanNode deletes the Kubernetes Node of a wiped machine that belonged to a cluster Ichor
// manages, as Remove node does; it returns a sentence for status.result, "" when the
// machine was of no managed cluster. The machine is in maintenance mode by now, so its kubelet cannot
// register the Node again. The Node is deleted only when its system UUID is the machine's: a Node of
// the same name that is somebody else's stays.
func (r *MachineReclaimReconciler) deleteOrphanNode(ctx context.Context, mr *ichorv1alpha1.MachineReclaim) string {
	in := mr.Status.Inspection
	if in == nil || in.ClusterID == "" || in.Hostname == "" {
		return ""
	}

	known, err := reclaim.LoadKnown(ctx, r.Client, r.ControllerNamespace)
	if err != nil || !slices.ContainsFunc(known.ClusterIDs, func(id string) bool { return strings.EqualFold(id, in.ClusterID) }) {
		return ""
	}

	name := strings.ToLower(in.Hostname)

	var node corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			return "; it had no Kubernetes Node to delete"
		}

		return fmt.Sprintf("; the Kubernetes Node %[1]s could not be read, run kubectl delete node %[1]s", name)
	}

	if !strings.EqualFold(node.Status.NodeInfo.SystemUUID, in.UUID) {
		return fmt.Sprintf("; the Kubernetes Node %[1]s was not deleted because its system UUID is not the node's, "+
			"run kubectl delete node %[1]s if it is the node's", name)
	}

	if err := r.Delete(ctx, &node, client.Preconditions{UID: &node.UID}); client.IgnoreNotFound(err) != nil {
		return fmt.Sprintf("; the Kubernetes Node %[1]s could not be deleted, run kubectl delete node %[1]s", name)
	}

	return fmt.Sprintf("; the Kubernetes Node %s was deleted", name)
}

func (r *MachineReclaimReconciler) fail(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, msg string) (ctrl.Result, error) {
	return r.end(ctx, mr, ichorv1alpha1.MachineReclaimFailed, "ReclaimFailed", msg)
}

func (r *MachineReclaimReconciler) expire(ctx context.Context, mr *ichorv1alpha1.MachineReclaim) (ctrl.Result, error) {
	return r.end(ctx, mr, ichorv1alpha1.MachineReclaimExpired, "ReclaimExpired",
		"the request expired before it was confirmed; nothing was sent to the node")
}

// end deletes the credential and records a terminal phase that did not succeed.
func (r *MachineReclaimReconciler) end(
	ctx context.Context, mr *ichorv1alpha1.MachineReclaim, phase ichorv1alpha1.MachineReclaimPhase, reason, msg string,
) (ctrl.Result, error) {
	r.deleteCredential(ctx, mr)

	mr.Status.Phase = phase
	mr.Status.Result = msg

	if err := r.Status().Update(ctx, mr); err != nil {
		return ctrl.Result{}, err
	}

	r.audit(mr, corev1.EventTypeWarning, reason, msg)

	return ctrl.Result{RequeueAfter: time.Second}, nil
}

// retain keeps a finished request as the record for seven days after its expiry time, makes sure
// its credential is gone, and then deletes it.
func (r *MachineReclaimReconciler) retain(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, now time.Time) (ctrl.Result, error) {
	if mr.Status.Audit.CredentialDeletedAt == nil {
		r.deleteCredential(ctx, mr)

		if err := r.Status().Update(ctx, mr); err != nil {
			return ctrl.Result{}, err
		}
	}

	at := mr.Status.ExpiresAt.Add(reclaimRetention)
	if now.Before(at) {
		return ctrl.Result{RequeueAfter: at.Sub(now)}, nil
	}

	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, mr))
}

// credential is what dials the node: the cluster's own admin credential for a request with
// spec.clusterCredentials (an orphan of that cluster, no pasted talosconfig), else the
// request's pasted Secret.
func (r *MachineReclaimReconciler) credential(ctx context.Context, mr *ichorv1alpha1.MachineReclaim) (reclaim.Credential, error) {
	if !mr.Spec.ClusterCredentials {
		return r.adoptCredential(ctx, mr)
	}

	if mr.Spec.ClusterRef == nil || mr.Spec.ClusterRef.Name == "" {
		return reclaim.Credential{}, errors.New("clusterCredentials needs spec.clusterRef")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, client.ObjectKey{Namespace: mr.Namespace, Name: mr.Spec.ClusterRef.Name}, &cluster); err != nil {
		return reclaim.Credential{}, fmt.Errorf("the cluster %q could not be read", mr.Spec.ClusterRef.Name)
	}

	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, &cluster)
	if err != nil {
		return reclaim.Credential{}, fmt.Errorf("the credentials of cluster %q could not be loaded", cluster.Name)
	}

	return reclaim.ClusterCredential(cluster.Name, bundle)
}

var errSecretPending = errors.New("the credential Secret does not exist yet")

// adoptCredential reads the request's credential Secret and makes the request its owner. It
// refuses a Secret that is not of the credential type, or that another request already owns:
// a credential serves one request.
func (r *MachineReclaimReconciler) adoptCredential(ctx context.Context, mr *ichorv1alpha1.MachineReclaim) (reclaim.Credential, error) {
	var sec corev1.Secret

	key := client.ObjectKey{Namespace: mr.Namespace, Name: mr.Spec.CredentialSecretRef}
	if err := r.Get(ctx, key, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return reclaim.Credential{}, errSecretPending
		}

		return reclaim.Credential{}, errors.New("the credential Secret could not be read")
	}

	if sec.Type != ichorv1alpha1.ReclaimCredentialSecretType {
		return reclaim.Credential{}, fmt.Errorf("the credential Secret %s is not of type %s", sec.Name, ichorv1alpha1.ReclaimCredentialSecretType)
	}

	for _, o := range sec.OwnerReferences {
		if o.Kind == reclaimKind && o.UID != mr.UID {
			return reclaim.Credential{}, errors.New("the credential Secret belongs to another request; a new request needs a new paste")
		}
	}

	if !ownedBy(&sec, mr) {
		sec.OwnerReferences = append(sec.OwnerReferences, metav1.OwnerReference{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: reclaimKind, Name: mr.Name, UID: mr.UID,
		})

		if err := r.Update(ctx, &sec); err != nil {
			return reclaim.Credential{}, errors.New("the credential Secret could not be claimed")
		}
	}

	cred := reclaim.NewCredential(sec.Data[ichorv1alpha1.ReclaimCredentialKey])
	if cred.Empty() {
		return reclaim.Credential{}, fmt.Errorf("the credential Secret %s has no %q key", sec.Name, ichorv1alpha1.ReclaimCredentialKey)
	}

	return cred, nil
}

func unclaimed(sec *corev1.Secret) bool {
	_, labelled := sec.Labels[ichorv1alpha1.ReclaimLabel]

	return labelled && !slices.ContainsFunc(sec.OwnerReferences, func(o metav1.OwnerReference) bool {
		return o.Kind == reclaimKind
	})
}

func ownedBy(sec *corev1.Secret, mr *ichorv1alpha1.MachineReclaim) bool {
	return slices.ContainsFunc(sec.OwnerReferences, func(o metav1.OwnerReference) bool { return o.UID == mr.UID })
}

// deleteCredential deletes the request's Secret if, and only if, it is of the credential type and
// owned by this request (or unclaimed and labelled), so a spec.credentialSecretRef pointing at any other Secret deletes
// nothing. It records the deletion in memory; the caller writes status.
func (r *MachineReclaimReconciler) deleteCredential(ctx context.Context, mr *ichorv1alpha1.MachineReclaim) {
	var sec corev1.Secret

	if mr.Spec.CredentialSecretRef == "" { // clusterCredentials: nothing was pasted
		if mr.Status.Audit.CredentialDeletedAt == nil {
			t := metav1.NewTime(nowFrom(r.Now))
			mr.Status.Audit.CredentialDeletedAt = &t
		}

		return
	}

	key := client.ObjectKey{Namespace: mr.Namespace, Name: mr.Spec.CredentialSecretRef}
	if err := r.Get(ctx, key, &sec); err != nil {
		if apierrors.IsNotFound(err) && mr.Status.Audit.CredentialDeletedAt == nil {
			t := metav1.NewTime(nowFrom(r.Now))
			mr.Status.Audit.CredentialDeletedAt = &t
		}

		return
	}

	// Ours: of the credential type and either owned by this request or not yet claimed by any.
	if sec.Type != ichorv1alpha1.ReclaimCredentialSecretType || (!ownedBy(&sec, mr) && !unclaimed(&sec)) {
		return
	}

	if err := r.Delete(ctx, &sec, client.Preconditions{UID: &sec.UID}); client.IgnoreNotFound(err) != nil {
		logf.FromContext(ctx).Info("could not delete a credential Secret; the sweeper will", "secret", sec.Name)

		return
	}

	t := metav1.NewTime(nowFrom(r.Now))
	mr.Status.Audit.CredentialDeletedAt = &t

	r.audit(mr, corev1.EventTypeNormal, "CredentialDeleted", "Secret "+sec.Name)
}

// audit emits an Event on the request and on the IchorInstallation (which outlives the request).
// Messages carry names, addresses and identities, never what is in a Secret.
func (r *MachineReclaimReconciler) audit(mr *ichorv1alpha1.MachineReclaim, eventType, reason, msg string) {
	if r.Recorder == nil {
		return
	}

	r.Recorder.Event(mr, eventType, reason, msg)

	var installs ichorv1alpha1.IchorInstallationList
	if err := r.List(context.Background(), &installs); err != nil {
		return
	}

	for i := range installs.Items {
		r.Recorder.Event(&installs.Items[i], eventType, reason, fmt.Sprintf("MachineReclaim %s/%s: %s", mr.Namespace, mr.Name, msg))
	}
}

// safeError describes a failure with the address, the stage and, for a Talos API error, the gRPC
// code. The error text itself is dropped: transport errors can quote what was sent. Refusals and
// confirmation mismatches are Ichor's own sentences and are kept.
func safeError(address, stage string, err error) string {
	msg := err.Error()
	for _, own := range []string{"refusing to reclaim:", "the typed "} {
		if strings.HasPrefix(msg, own) {
			return msg
		}
	}

	if st, ok := status.FromError(err); ok && st.Code() != 0 {
		return fmt.Sprintf("%s: %s failed (Talos API: %s)", address, stage, st.Code())
	}

	return fmt.Sprintf("%s: %s failed", address, stage)
}

func inspectionStatus(in reclaim.Inspection, plan reclaim.Plan) *ichorv1alpha1.MachineReclaimInspection {
	role := "worker"
	if in.ControlPlane {
		role = "controlplane"
	}

	out := &ichorv1alpha1.MachineReclaimInspection{
		UUID: in.UUID, Hostname: in.Hostname, Role: role, TalosVersion: in.Version,
		ClusterName: in.ClusterName, ClusterID: in.ClusterID, ClusterReachable: in.ClusterReachable,
		Graceful: plan.Graceful, NeedsForce: plan.NeedsForce,
		Refusals: plan.Refusals, Warnings: plan.Warnings,
	}

	for _, m := range in.EtcdMembers {
		out.EtcdMembers = append(out.EtcdMembers, ichorv1alpha1.MachineReclaimEtcdMember{
			ID: m.ID, Hostname: m.Hostname, Healthy: !m.Unhealthy,
		})
	}

	for _, v := range in.Volumes {
		out.Volumes = append(out.Volumes, ichorv1alpha1.MachineReclaimVolume{
			Name: v.Name, Location: v.Location, SizeBytes: v.Size, Encrypted: v.Encrypted,
		})
	}

	return out
}

// SetupWithManager sets up the controller and the credential sweeper with the Manager.
func (r *MachineReclaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.Add(&ReclaimCredentialSweeper{Client: mgr.GetClient(), Namespace: r.ControllerNamespace}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.MachineReclaim{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("machinereclaim").
		// A request waits on a node that may be gone; four workers keep one from blocking the rest.
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}
