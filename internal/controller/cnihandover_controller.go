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
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// argoApplicationGVK identifies Argo CD's Application CRD (argoproj.io/v1alpha1, per
// pkg/gitops's own Application type and its doc.go). Read as unstructured.Unstructured --
// the standard controller-runtime pattern for a foreign CRD this project neither owns nor
// vendors Go types for -- rather than adding a dependency on Argo CD's module for three
// status fields.
var argoApplicationGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}

// handoverRequeueInterval is how often CNIHandoverReconciler re-checks the CNI's Argo CD
// Application while waiting: spec.cni.handover is "automatic" but the Application is not
// yet Synced and Healthy, or does not exist at all (Argo CD not installed, or not yet
// adopted it). Thirty seconds: a hand-over is a one-time bootstrap event an operator is
// watching for, so it is checked more often than DiscoverAll's own five-minute background
// poll (internal/controller/taloscluster_controller.go).
const handoverRequeueInterval = 30 * time.Second

const (
	reasonHandoverDisabled       = "Disabled"
	reasonHandoverManualPending  = "ManualPending"
	reasonHandoverWaitingForArgo = "WaitingForArgoCD"
	reasonHandoverWaitingForSync = "WaitingForSync"
	reasonHandoverComplete       = "Complete"
	reasonCNIPresetRemoved       = "PresetRemoved"
)

// CNIHandoverReconciler implements the one real member of what
// docs/design/architecture.md's "GitOps and Argo CD" section once called "one mechanism
// with a component registry, not three special cases": the CNI's
// inject-through-cluster.inlineManifests-then-remove path. Ichor itself is applied
// directly through the API server, never inlined, and Argo CD is never installed by Ichor
// at all (ADR 0019) -- so neither one ever needs this path, and there is nothing left to
// register. ADR 0024 records why that means no registry abstraction here, for one member.
//
// A separate controller from TalosClusterReconciler on purpose: that reconciler's own doc
// comment already warns DiscoverAll can block a TalosCluster's reconcile for up to ~68s at
// spec.discovery's caps, and a hand-over check waiting behind that would make the "checked
// every 30s" promise above false whenever discovery is also configured. Two controllers
// watching the same Kind each get their own work queue, so a slow discovery pass in one
// never delays the other -- raising TalosClusterReconciler's MaxConcurrentReconciles would
// not have helped, since a single TalosCluster only ever has one in-flight reconcile
// through one controller's queue regardless of that setting (ADR 0024).
type CNIHandoverReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch

// Reconcile checks whether cluster's CNI Application is Synced and Healthy and, if so
// (and spec.cni.handover is "automatic", the default), records that on cluster.status --
// which is what pkg/render.BuildConfig reads to stop rendering the CNI's
// cluster.inlineManifests entry. Removal has to happen there, not by rewriting a
// TalosConfigPatch's spec (principle 5: the controller renders from ingredients, it does
// not rewrite them) -- and it has to happen at all, because Talos's own
// ManifestApplyController re-applies cluster.inlineManifests during a Kubernetes upgrade
// and has no delete path, so a stale entry rolls the CNI back to whatever Ichor originally
// inlined it as (docs/design/architecture.md, "GitOps and Argo CD").
//
// A cluster whose preset is not cilium (ADR 0099) has nothing to hand over; it only gets the
// CNIUnsupported condition.
func (r *CNIHandoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	preset, ok := cniPresetFor(cluster.Spec.CNI.Preset)
	if !ok {
		return ctrl.Result{}, r.markCNIUnsupported(ctx, &cluster)
	}

	app, err := preset.ArgoCDApplication(cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy))
	if err != nil {
		return ctrl.Result{}, err
	}
	if app == nil {
		return ctrl.Result{}, nil
	}

	switch cluster.Spec.CNI.Handover {
	case "disabled":
		return ctrl.Result{}, r.setHandoverCondition(ctx, &cluster, metav1.ConditionFalse, reasonHandoverDisabled,
			`spec.cni.handover is "disabled"; the cluster.inlineManifests entry is never removed automatically`)
	case "manual":
		return ctrl.Result{}, r.setHandoverCondition(ctx, &cluster, metav1.ConditionFalse, reasonHandoverManualPending,
			`spec.cni.handover is "manual"; waiting for an operator-triggered hand-over (ichor gitops handoff cni)`)
	}

	// "automatic", including the CRD default's zero value not yet reached by an older
	// object -- treated the same way driftPolicy's own zero value is: the safe side,
	// which here is the one that still does the work rather than silently skipping it.
	var live unstructured.Unstructured
	live.SetGroupVersionKind(argoApplicationGVK)

	err = r.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, &live)
	switch {
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		// Either Argo CD has not adopted the Application yet, or its CRDs are not
		// installed on this cluster at all -- indistinguishable here, and both are
		// "not here yet", not an error. A cluster that simply does not run Argo CD
		// should set spec.cni.handover: disabled to silence this; left at the
		// "automatic" default, this polls forever and reports it plainly, which is
		// "does not break" (nothing blocks, nothing retries in a tight loop), not
		// "hangs".
		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setHandoverCondition(ctx, &cluster, metav1.ConditionFalse, reasonHandoverWaitingForArgo,
			fmt.Sprintf("no Argo CD Application %q found in namespace %q yet", app.Name, app.Namespace))
	case err != nil:
		return ctrl.Result{}, err
	}

	syncStatus, healthStatus := argoApplicationStatus(&live)
	if syncStatus != "Synced" || healthStatus != "Healthy" {
		return ctrl.Result{RequeueAfter: handoverRequeueInterval}, r.setHandoverCondition(ctx, &cluster, metav1.ConditionFalse, reasonHandoverWaitingForSync,
			fmt.Sprintf("Argo CD reports %q sync=%q health=%q", app.Name, syncStatus, healthStatus))
	}

	return ctrl.Result{}, r.setHandoverCondition(ctx, &cluster, metav1.ConditionTrue, reasonHandoverComplete,
		fmt.Sprintf("Argo CD reports %q Synced and Healthy; no longer rendering its cluster.inlineManifests entry", app.Name))
}

// argoApplicationStatus reads an Argo CD Application's sync and health status strings
// (status.sync.status, status.health.status) out of app. A missing field (not yet
// populated, or not shaped the way expected) reads back as "", which never equals
// "Synced"/"Healthy", so it is treated as not-ready rather than an error.
func argoApplicationStatus(app *unstructured.Unstructured) (sync, health string) {
	sync, _, _ = unstructured.NestedString(app.Object, "status", "sync", "status")
	health, _, _ = unstructured.NestedString(app.Object, "status", "health", "status")

	return sync, health
}

// setHandoverCondition persists render.ConditionCNIHandover on cluster.
func (r *CNIHandoverReconciler) setHandoverCondition(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, status metav1.ConditionStatus, reason, message string,
) error {
	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: render.ConditionCNIHandover, Status: status, Reason: reason, Message: message,
	})

	return r.Status().Update(ctx, cluster)
}

// markCNIUnsupported sets CNIUnsupported=True on a cluster whose preset is not cilium (ADR 0099).
// The controllers refuse to render it and change nothing on its nodes; the condition says why.
func (r *CNIHandoverReconciler) markCNIUnsupported(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) error {
	if cluster.Spec.CNI.Preset == "" {
		return nil
	}

	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ichorv1alpha1.ConditionCNIUnsupported, Status: metav1.ConditionTrue, Reason: reasonCNIPresetRemoved,
		Message: fmt.Sprintf("spec.cni.preset %q was removed in ADR 0099 (Cilium is the only CNI); "+
			"no configuration is rendered or applied; delete and recreate the cluster", cluster.Spec.CNI.Preset),
	})

	return r.Status().Update(ctx, cluster)
}

// cniPresetFor resolves cluster.Spec.CNI.Preset to its cni.Preset implementation. Cilium is the
// only one (ADR 0099). Duplicates internal/cli's own cniPreset (this package cannot import
// internal/cli).
func cniPresetFor(preset string) (cni.Preset, bool) {
	if preset == ciliumName {
		return cilium.New(), true
	}

	return nil, false
}

// SetupWithManager sets up the controller with the Manager.
func (r *CNIHandoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Named("cnihandover").
		Complete(r)
}
