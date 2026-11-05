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
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/values"
)

const (
	// handoverRequeueInterval is how often CNIHandoverReconciler re-checks while it waits for
	// Cilium to come up, for the cluster operation lock, or for a release record: a hand-over
	// is a one-time bootstrap event an operator is watching for.
	handoverRequeueInterval = 30 * time.Second

	// cniJobPollInterval is how often it looks at a running Helm Job. It also keeps the cluster
	// operation lock (a five-minute lease) renewed, so it must stay far below that.
	cniJobPollInterval = 15 * time.Second

	// cniJobRetryInterval is how long a failed Cilium Helm Job stands before a new one replaces it.
	cniJobRetryInterval = time.Minute

	// cniHelmLockIdentity holds the cluster operation lock while a Cilium Helm Job runs. It is
	// not controllerIdentity on purpose: Acquire lets the same identity back in, so with the
	// shared one a node rollout holding the lock would not stop the Job.
	cniHelmLockIdentity = "ichor-controller/cni-helm"

	// helmOwnerLabel is the label Helm's Secrets driver puts on its release records.
	helmOwnerLabel = "owner"

	// helmStatusDeployed is the status of a release record that is the live one.
	helmStatusDeployed = "deployed"
)

const (
	reasonHandoverWaitingForCilium = "WaitingForCilium"
	reasonHandoverAdopting         = "AdoptingCilium"
	reasonHandoverComplete         = "Complete"
	reasonCNIPresetRemoved         = "PresetRemoved"
	reasonCNIJobRunning            = "Applying"
	reasonCNIJobFailed             = "JobFailed"
	reasonCNIWaitingForLock        = "WaitingForLock"
	reasonCNIWaitingForRelease     = "WaitingForRelease"
	reasonCNIInvalidValues         = "InvalidValues"
	reasonCNIApplied               = "Applied"
	reasonCNITunnelMismatch        = "TunnelMismatch"
	tunnelMode                     = "tunnel"
)

// CNIHandoverReconciler hands the bootstrapped Cilium to Ichor (ADR 0099, decision 2) and keeps
// the Helm release at the cluster's Cilium values afterwards. Cilium arrives as an inline
// manifest; once the control plane is up, a one-shot Helm Job (AdvanceCNIHelmJob) adopts it into
// the Helm release "cilium". When that Job has completed, the release record is deployed at the
// effective values and the DaemonSet is fully ready, the reconciler sets CNIHandover=True,
// which is what pkg/render reads to stop rendering the CNI's inline manifest entry (Talos only
// creates inline manifests, never updates them, so a stale entry must go; ADR 0024). A later
// change to spec.cni.cilium.values is a new effective-values hash and so a new Job.
//
// While a Job runs the reconciler holds the cluster operation lock (pkg/clusterlock), so it never
// overlaps a node rollout, and a node rollout never starts under it.
//
// A separate controller from TalosClusterReconciler on purpose: that reconciler's own doc
// comment warns DiscoverAll can block a reconcile for up to ~68s, and a hand-over check waiting
// behind that would make the cadence above false (ADR 0024).
type CNIHandoverReconciler struct {
	client.Client

	// JobRetryInterval is how long a failed Helm Job stands before a new one replaces it
	// (0: a minute).
	JobRetryInterval time.Duration

	// APIReader reads the release record, the lock and the control-plane count live (the manager's
	// uncached reader); falls back to Client when nil.
	APIReader client.Reader

	// Kube reads the Job's pod log for the failure condition; nil leaves the Job's own reason.
	Kube kubernetes.Interface

	// Namespace is where the Helm release record lives (ichor-system); the Job runs in kube-system.
	Namespace string

	// Images pins the Cilium Job's installer image to a verified digest (ADR 0122); nil refuses to start any Job.
	Images InstallerImages
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=ichorinstallations,verbs=get;list;watch
// The values re-check before a Helm Job reads whether the Gateway API CRDs exist, one CRD by name (ADR 0099, decision 4).
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,resourceNames=gateways.gateway.networking.k8s.io,verbs=get

// The Helm Job runs in kube-system (cniJobNamespace); the controller's namespaced Role there is
// hand-written (config/rbac/ichor-cni/controller_role.yaml): controller-gen's shared
// "manager-role" name would collide with this one once kustomize sets every namespace.

// Reconcile runs the flow in the type's comment. A cluster whose preset is not cilium has nothing
// to hand over; it only gets the CNIUnsupported condition.
func (r *CNIHandoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Ichor leaves an unmanaged cluster's CNI alone: no hand-over, no Job, no conditions (ADR 0103).
	if cluster.Spec.CNI.Preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return ctrl.Result{}, nil
	}

	if cluster.Spec.CNI.Preset != ciliumName {
		return ctrl.Result{}, r.markCNIUnsupported(ctx, &cluster)
	}

	// The mode Cilium runs now, not spec.cni.kubeProxy: a spec edit alone must not flip Cilium. Only
	// the kube-proxy switch operation applies the spec's mode (ADR 0066).
	mode, err := currentKubeProxyMode(ctx, r.reader(), &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	nodes, err := clusterNodeCount(ctx, r.reader())
	if err != nil {
		return ctrl.Result{}, err
	}

	hash, err := CNIValuesHash(&cluster, mode, nodes)
	if err != nil {
		// A values change fixes it and triggers a new reconcile; retrying now would not.
		return ctrl.Result{}, r.setCondition(ctx, &cluster, ichorv1alpha1.ConditionCNIValuesApplied,
			metav1.ConditionFalse, reasonCNIInvalidValues, err.Error())
	}

	handedOver := apimeta.IsStatusConditionTrue(cluster.Status.Conditions, render.ConditionCNIHandover)
	if handedOver {
		// Whether the embedded chart is newer than the release's (ADR 0099, decision 11).
		if err := r.syncUpgradeAvailable(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	if handedOver && cluster.Status.CNI != nil && cluster.Status.CNI.AppliedValuesHash == hash {
		// Cilium already runs these values, so an earlier refused or failed edit that was reverted to
		// them is over: report the values applied again and forget the failures of that attempt. Status
		// is written only when it changes.
		if recordApplied(&cluster) {
			return ctrl.Result{}, r.Status().Update(ctx, &cluster)
		}

		return ctrl.Result{}, nil
	}

	// Before the first Job the bootstrapped Cilium has to be up: the adoption waits for it, and a
	// Job on a cluster with no CNI yet would only sit there.
	if !handedOver {
		if ready, why, err := r.ciliumReady(ctx); err != nil {
			return ctrl.Result{}, err
		} else if !ready {
			return ctrl.Result{RequeueAfter: handoverRequeueInterval},
				r.setCondition(ctx, &cluster, render.ConditionCNIHandover, metav1.ConditionFalse, reasonHandoverWaitingForCilium, why)
		}
	}

	// The tunnel, routing mode and IPAM of a running Cilium cannot be changed under live pods: a
	// Job that did would leave the release half-upgraded and the cluster without a network.
	if mismatch, err := r.networkMismatch(ctx, &cluster, mode); err != nil {
		return ctrl.Result{}, err
	} else if mismatch != "" {
		return ctrl.Result{RequeueAfter: handoverRequeueInterval},
			r.setProgress(ctx, &cluster, handedOver, reasonCNITunnelMismatch, mismatch)
	}

	// Ichor's own record is already deployed at this hash (`ichor adopt` installed Cilium itself on
	// a cluster with no CNI, ADR 0104 1.5): there is nothing for a Job to change, so no Job runs.
	// An adopted cluster's own Cilium (inline manifest annotation) still goes through the Job,
	// which retires the foreign record.
	if !handedOver && cluster.Annotations[ichorv1alpha1.TalosClusterCNIInlineManifestAnnotation] == "" {
		rel, err := readCiliumRelease(ctx, r.reader(), r.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}

		if rel.Status == helmStatusDeployed && rel.ValuesHash == hash {
			// A Job that was running when the release landed may still hold the lock.
			if err := r.lock(&cluster).Release(ctx, cniHelmLockIdentity); err != nil {
				return ctrl.Result{}, err
			}

			return r.finish(ctx, &cluster, hash, handedOver)
		}
	}

	return r.runJob(ctx, &cluster, mode, nodes, hash, handedOver)
}

// networkMismatch compares the tunnel protocol, routing mode and IPAM mode the effective values
// would render with the running cilium-config, and says what differs ("" when nothing does or
// there is no cilium-config yet).
func (r *CNIHandoverReconciler) networkMismatch(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode,
) (string, error) {
	var cm corev1.ConfigMap

	switch err := r.reader().Get(ctx, types.NamespacedName{Namespace: kubeSystemNamespace, Name: ciliumConfigName}, &cm); {
	case apierrors.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", err
	}

	vals, err := cilium.EffectiveValuesForMode(cluster, mode)
	if err != nil {
		return "", nil //nolint:nilerr // invalid values are reported by the hash step
	}

	str := func(path, def string) string {
		if v, ok := values.Get(vals, path); ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}

		return def
	}

	for _, k := range []struct{ key, path, def, what string }{
		{"tunnel-protocol", "tunnelProtocol", "vxlan", tunnelMode},
		{"routing-mode", "routingMode", tunnelMode, "routing mode"},
		{"ipam", "ipam.mode", "cluster-pool", "IPAM mode"},
	} {
		want, live := str(k.path, k.def), cm.Data[k.key]
		if live == "" || live == want {
			continue
		}

		if k.what == tunnelMode {
			return fmt.Sprintf("spec.cni.tunnel is %s but the running Cilium uses %s; Ichor does not change the tunnel of a running network",
				want, live), nil
		}

		return fmt.Sprintf("the values would set the %s to %s but the running Cilium uses %s; Ichor does not change it on a running network",
			k.what, want, live), nil
	}

	return "", nil
}

// runJob advances the Helm Job for hash under the cluster operation lock, and on completion
// records the result.
func (r *CNIHandoverReconciler) runJob(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode, nodes int, hash string, handedOver bool,
) (ctrl.Result, error) {
	inst, err := installerSource(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	lock := r.lock(cluster)

	if err := r.acquireDisruptiveLock(ctx, cluster, lock); err != nil {
		var held *clusterlock.HeldError

		var transitioning *transitioningError

		var shutting *shuttingDownError

		if errors.As(err, &held) || errors.As(err, &transitioning) || errors.As(err, &shutting) {
			return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setProgress(ctx, cluster, handedOver,
				reasonCNIWaitingForLock, "waiting for the cluster operation lock before changing Cilium: "+err.Error())
		}

		return ctrl.Result{}, err
	}

	done, err := AdvanceCNIHelmJob(ctx, r.Client, r.Namespace, cluster, mode, nodes, inst, r.Images,
		cniValuesCheck(r.Client, r.reader(), r.Namespace, cluster, mode), "", time.Time{})

	var failed *CNIHelmJobFailedError

	var invalid *CNIInvalidValuesError

	var image *installerImageError

	switch {
	case errors.As(err, &image):
		// The installer image was not resolved or verified, so no Job was started (ADR 0122): let go of the lock
		// and try again later, the registry or a pin may have been fixed.
		if err := lock.Release(ctx, cniHelmLockIdentity); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setProgress(ctx, cluster, handedOver, image.reason, image.Error())
	case errors.As(err, &invalid):
		// Nothing was started: let go of the lock and wait for an edit (of the values or of what they are
		// checked against) to make them valid. The spec is not touched.
		if err := lock.Release(ctx, cniHelmLockIdentity); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setProgress(ctx, cluster, handedOver, reasonCNIInvalidValues, invalid.Error())
	case errors.As(err, &failed):
		_ = lock.Release(ctx, cniHelmLockIdentity)

		res := ctrl.Result{}
		msg := r.failureMessage(ctx, failed)

		if failed.NoPod {
			// The Job is gone; try again later, whatever was refusing its pod may have been fixed.
			res.RequeueAfter = handoverRequeueInterval
		} else {
			var failures int32
			if res.RequeueAfter, failures, err = r.retryFailedJob(ctx, cluster, hash); err != nil {
				return ctrl.Result{}, err
			}

			msg += fmt.Sprintf("; failed %d %s in a row with these values, next attempt in %s",
				failures, map[bool]string{true: "time", false: "times"}[failures == 1], res.RequeueAfter.Round(time.Second))
		}

		return res, r.setProgress(ctx, cluster, handedOver, reasonCNIJobFailed, msg)
	case err != nil:
		return ctrl.Result{}, err
	case !done:
		return ctrl.Result{RequeueAfter: cniJobPollInterval}, r.setProgress(ctx, cluster, handedOver,
			reasonCNIJobRunning, "the Cilium Helm Job is running")
	}

	// The Job has completed: the lock is no longer needed, whatever the checks below say.
	if err := lock.Release(ctx, cniHelmLockIdentity); err != nil {
		return ctrl.Result{}, err
	}

	return r.finish(ctx, cluster, hash, handedOver)
}

// cniJobRetryCap is the longest wait between two attempts of a Helm Job that keeps failing.
const cniJobRetryCap = 30 * time.Minute

// cniRetryBackoff is how long a failed Cilium Helm Job stands before it is replaced, after failures
// failures in a row with the same values: base, doubling with each, up to cniJobRetryCap.
func cniRetryBackoff(base time.Duration, failures int32) time.Duration {
	d := base
	for range max(failures, 1) - 1 {
		if d = 2 * d; d >= cniJobRetryCap {
			return max(cniJobRetryCap, base)
		}
	}

	return d
}

// retryFailedJob schedules the next attempt after a Job failed: a failure may be transient (a
// pending record another run left behind, a node restarting), and nothing else would ever look at
// it again. Once the Job has been failed for the backoff (cniRetryBackoff: the retry interval,
// doubling with each failure of the same values) it is deleted, so the next pass creates a fresh
// one (the failure stays in the condition until that Job reports). A failing Job otherwise holds the
// cluster lock for its whole run, over and over (ADR 0113 amendment 2026-11-04). The failure count
// lives in status.cni.jobFailures, updated when the failed Job is removed; another values hash is
// another Job and counts from zero. Returns when to reconcile again and how often these values have
// failed in a row, the Job just seen included.
func (r *CNIHandoverReconciler) retryFailedJob(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, hash string,
) (time.Duration, int32, error) {
	interval := cmp.Or(r.JobRetryInterval, cniJobRetryInterval)

	var retired int32
	if s := cluster.Status.CNI; s != nil && s.JobFailuresHash == hash {
		retired = s.JobFailures
	}

	var job batchv1.Job

	err := r.reader().Get(ctx, types.NamespacedName{Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash)}, &job)
	if err != nil {
		return cniRetryBackoff(interval, retired), retired, client.IgnoreNotFound(err)
	}

	failures := retired + 1

	if wait := cniRetryBackoff(interval, failures) - time.Since(jobEnded(&job)); wait > 0 {
		return wait, failures, nil
	}

	st := cniStatus(cluster)
	st.JobFailures, st.JobFailuresHash = failures, hash
	if err := r.Status().Update(ctx, cluster); err != nil {
		return 0, failures, err
	}

	return time.Second, failures, deleteCNIHelmJob(ctx, r.Client, &job)
}

// finish checks the Helm release and the DaemonSet after a completed Job and records the hand-over.
func (r *CNIHandoverReconciler) finish(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, hash string, handedOver bool,
) (ctrl.Result, error) {
	rel, err := readCiliumRelease(ctx, r.reader(), r.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch {
	case rel.Status == helmStatusDeployed && rel.ValuesHash != hash:
		// A finished Job for this hash is still around (its TTL is a day) from before the values
		// moved away and back: it proves nothing about the release now. Remove it so the next
		// pass runs a new one.
		stale := &batchv1.Job{Namespace: cniJobNamespace, Name: cniHelmJobName(cluster, hash)}
		if err := deleteCNIHelmJob(ctx, r.Client, stale); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: cniJobPollInterval}, r.setProgress(ctx, cluster, handedOver, reasonCNIWaitingForRelease,
			"the Helm release is deployed at other values than the cluster's; running the Helm Job again")
	case rel.Status != helmStatusDeployed:
		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setProgress(ctx, cluster, handedOver, reasonCNIWaitingForRelease,
			fmt.Sprintf("the Helm Job completed but release %s has status %q, not deployed", cilium.ReleaseName, rel.Status))
	}

	if ready, why, err := r.ciliumReady(ctx); err != nil {
		return ctrl.Result{}, err
	} else if !ready {
		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setProgress(ctx, cluster, handedOver, reasonHandoverWaitingForCilium, why)
	}

	st := cniStatus(cluster)
	st.AppliedValuesHash = hash
	recordApplied(cluster)

	// An adopted cluster's own Cilium inline manifest entry (named by `ichor adopt` in an
	// annotation) is recorded in the same update that sets CNIHandover, so render drops it exactly
	// when it drops the bootstrap entry, never before the takeover has succeeded (ADR 0103, 1.2).
	if name := cluster.Annotations[ichorv1alpha1.TalosClusterCNIInlineManifestAnnotation]; name != "" {
		st.TakenOverInlineManifest = name
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: reasonHandoverComplete,
		Message: fmt.Sprintf("Helm release %s is deployed and Cilium is ready; no longer rendering its cluster.inlineManifests entry",
			cilium.ReleaseName),
	})

	return ctrl.Result{}, r.Status().Update(ctx, cluster)
}

// recordApplied sets CNIValuesApplied to True and clears the failure count of the last attempt, for
// the values the status records as applied, and reports whether anything changed.
func recordApplied(cluster *ichorv1alpha1.TalosCluster) bool {
	changed := apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionCNIValuesApplied, Status: metav1.ConditionTrue, Reason: reasonCNIApplied,
		Message: "the Cilium Helm release is deployed at the cluster's values and the DaemonSet is ready",
	})

	if st := cluster.Status.CNI; st != nil && (st.JobFailures != 0 || st.JobFailuresHash != "") {
		st.JobFailures, st.JobFailuresHash = 0, ""
		changed = true
	}

	return changed
}

// setProgress records a not-yet-done state. Before the hand-over it is the CNIHandover condition
// that carries it; afterwards CNIHandover stays True (flipping it back would re-render the
// inline manifest) and only CNIValuesApplied reports.
func (r *CNIHandoverReconciler) setProgress(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, handedOver bool, reason, message string,
) error {
	if !handedOver {
		handoverReason := reason
		if reason == reasonCNIJobRunning {
			handoverReason = reasonHandoverAdopting
		}

		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionFalse, Reason: handoverReason, Message: message,
		})
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionCNIValuesApplied, Status: metav1.ConditionFalse, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, cluster)
}

// failureMessage is the Job's last log line, or its own reason when the log cannot be read.
func (r *CNIHandoverReconciler) failureMessage(ctx context.Context, failed *CNIHelmJobFailedError) string {
	if failed.NoPod {
		return fmt.Sprintf("Cilium Helm Job %s created no pod within %s: %s", failed.JobName, cniPodGrace,
			cmp.Or(r.failedCreateMessage(ctx, failed.JobName), failed.Message))
	}

	if line := r.lastLogLine(ctx, failed.JobName); line != "" {
		return fmt.Sprintf("Cilium Helm Job %s failed: %s", failed.JobName, line)
	}

	return fmt.Sprintf("Cilium Helm Job %s failed: %s %s", failed.JobName, failed.Reason, failed.Message)
}

// failedCreateMessage is the newest FailedCreate event the Job controller recorded for jobName
// (e.g. the admission refusal of its pod), or "" when there is none or it cannot be read.
func (r *CNIHandoverReconciler) failedCreateMessage(ctx context.Context, jobName string) string {
	var events corev1.EventList
	if err := r.reader().List(ctx, &events, client.InNamespace(cniJobNamespace)); err != nil {
		return ""
	}

	var (
		msg    string
		newest time.Time
	)

	for i := range events.Items {
		e := &events.Items[i]
		if e.InvolvedObject.Kind != "Job" || e.InvolvedObject.Name != jobName || e.Reason != "FailedCreate" {
			continue
		}

		if at := eventTime(e); msg == "" || at.After(newest) {
			msg, newest = e.Message, at
		}
	}

	return msg
}

func eventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	}

	return e.CreationTimestamp.Time
}

func (r *CNIHandoverReconciler) lastLogLine(ctx context.Context, jobName string) string {
	if r.Kube == nil {
		return ""
	}

	var pods corev1.PodList
	if err := r.reader().List(ctx, &pods, client.InNamespace(cniJobNamespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}

	for i := range pods.Items {
		raw, err := r.Kube.CoreV1().Pods(cniJobNamespace).GetLogs(pods.Items[i].Name, &corev1.PodLogOptions{TailLines: ptr.To[int64](10)}).DoRaw(ctx)
		if err != nil {
			continue
		}

		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if line := strings.TrimSpace(lines[len(lines)-1]); line != "" {
			return line
		}
	}

	return ""
}

// ciliumReady reports whether the cilium DaemonSet in kube-system is fully rolled out.
func (r *CNIHandoverReconciler) ciliumReady(ctx context.Context) (ready bool, why string, err error) {
	var ds appsv1.DaemonSet

	err = r.reader().Get(ctx, types.NamespacedName{Namespace: cilium.ReleaseNamespace, Name: ciliumName}, &ds)

	switch {
	case apierrors.IsNotFound(err):
		return false, "the cilium DaemonSet does not exist yet", nil
	case err != nil:
		return false, "", err
	}

	if !daemonSetRolledOut(&ds) {
		return false, fmt.Sprintf("the cilium DaemonSet has %d of %d pods ready", ds.Status.NumberReady, ds.Status.DesiredNumberScheduled), nil
	}

	return true, "", nil
}

// ciliumRelease is what the newest Helm release record for cilium says.
type ciliumRelease struct {
	// Status is the record's status ("deployed", "failed", ...); empty when there is no record.
	Status string
	// ValuesHash is the hash of the values the release was deployed with.
	ValuesHash string
	// ChartVersion is the version of the chart the release was deployed from.
	ChartVersion string
}

// readCiliumRelease reads the newest release record of cilium in ns.
func readCiliumRelease(ctx context.Context, reader client.Reader, ns string) (ciliumRelease, error) {
	var secrets corev1.SecretList
	if err := reader.List(ctx, &secrets, client.InNamespace(ns),
		client.MatchingLabels{helmOwnerLabel: "helm", labelName: cilium.ReleaseName}); err != nil {
		return ciliumRelease{}, err
	}

	var (
		newest *corev1.Secret
		latest = -1
	)

	for i := range secrets.Items {
		v, convErr := strconv.Atoi(secrets.Items[i].Labels["version"])
		if convErr == nil && v > latest {
			newest, latest = &secrets.Items[i], v
		}
	}

	if newest == nil {
		return ciliumRelease{}, nil
	}

	vals, chartVersion, err := decodeRelease(newest.Data["release"])
	if err != nil {
		return ciliumRelease{}, fmt.Errorf("reading Helm release record %s: %w", newest.Name, err)
	}

	hash, err := hashCiliumValues(vals)

	return ciliumRelease{Status: newest.Labels["status"], ValuesHash: hash, ChartVersion: chartVersion}, err
}

// decodeRelease reads the user-supplied values and the chart version out of a Helm release record
// (Helm stores base64 of gzipped JSON). Helm's own decoder is unexported.
func decodeRelease(data []byte) (config map[string]any, chartVersion string, err error) {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, "", err
	}

	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, "", err
		}

		if raw, err = io.ReadAll(zr); err != nil {
			return nil, "", err
		}
	}

	var rel struct {
		Config map[string]any `json:"config"`
		Chart  struct {
			Metadata struct {
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		return nil, "", err
	}

	if rel.Config == nil {
		rel.Config = map[string]any{}
	}

	return rel.Config, rel.Chart.Metadata.Version, nil
}

func (r *CNIHandoverReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}

	return r.Client
}

func (r *CNIHandoverReconciler) lock(cluster *ichorv1alpha1.TalosCluster) *clusterlock.Lock {
	return clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0).WithReader(r.APIReader)
}

// acquireDisruptiveLock takes (or renews) the cluster operation lock for the Helm Job. Named like
// the other call sites because TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite requires it, and
// it makes the same refusals: a cluster being shut down, or mid control-plane replacement.
func (r *CNIHandoverReconciler) acquireDisruptiveLock(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, lock *clusterlock.Lock,
) error {
	if err := shutdownBlocker(ctx, r.reader(), cluster.Namespace, cluster.Name, nil); err != nil {
		return err
	}

	controlPlanes, err := deleteguard.CountApprovedControlPlanes(ctx, r.reader(), cluster.Namespace, cluster.Name)
	if err != nil {
		return err
	}

	if transitioningControlPlanes(controlPlanes) {
		return &transitioningError{controlPlanes: controlPlanes}
	}

	return lock.Acquire(ctx, cniHelmLockIdentity)
}

// setCondition persists one condition on cluster.
func (r *CNIHandoverReconciler) setCondition(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, condType string, status metav1.ConditionStatus, reason, message string,
) error {
	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, cluster)
}

// markCNIUnsupported sets CNIUnsupported=True on a cluster whose preset is not cilium (ADR 0099).
// The controllers refuse to render it and change nothing on its nodes; the condition says why.
func (r *CNIHandoverReconciler) markCNIUnsupported(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) error {
	if cluster.Spec.CNI.Preset == "" {
		return nil
	}

	return r.setCondition(ctx, cluster, ichorv1alpha1.ConditionCNIUnsupported, metav1.ConditionTrue, reasonCNIPresetRemoved,
		fmt.Sprintf("spec.cni.preset %q was removed in ADR 0099 (Cilium is the only CNI); "+
			"no configuration is rendered or applied; delete and recreate the cluster", cluster.Spec.CNI.Preset))
}

// allClusters enqueues every TalosCluster: the Cilium values follow the number of Nodes (CNIValuesHash),
// so a Node that appears or goes away may change them, and nothing else would look again once the
// values were applied.
func (r *CNIHandoverReconciler) allClusters(ctx context.Context, _ client.Object) []reconcile.Request {
	var clusters ichorv1alpha1.TalosClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(clusters.Items))
	for i := range clusters.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&clusters.Items[i])})
	}

	return reqs
}

// SetupWithManager sets up the controller with the Manager.
func (r *CNIHandoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Watches(&ichorv1alpha1.TalosOperation{}, handler.EnqueueRequestsFromMapFunc(cniUpgradeOperationClusters)).
		// Only a Node coming or going counts; its heartbeats and status updates do not.
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.allClusters), builder.WithPredicates(predicate.Funcs{
			UpdateFunc:  func(event.UpdateEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		})).
		Named("cnihandover").
		Complete(r)
}
