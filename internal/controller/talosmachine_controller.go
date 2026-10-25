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

// Package controller holds Ichor's controllers. TalosMachineReconciler
// implements "Configuration reconcile" (docs/design/architecture.md 5.6):
// it renders a machine's configuration (pkg/render), compares it with what
// the node is currently running, and -- depending on what pkg/fieldmeta
// says the difference means -- applies it, refuses with a clear reason, or
// does nothing. It never writes TalosMachine.spec (principle 5); status
// holds only hashes and condition state (principle 3), never the rendered
// configuration itself.
//
// A hash mismatch alone cannot tell a desired-state change (the user edited
// a TalosConfigPatch) apart from drift (someone changed the node): both
// produce one. Reconcile tells them apart with desiredInputsHash, a
// fingerprint of the generations of TalosCluster, this TalosMachine and
// every applicable TalosConfigPatch, recorded in
// TalosMachine.status.observedInputsHash only when the node is confirmed to
// match what was rendered. If the current fingerprint still matches the
// recorded one, nothing that feeds the render has moved, so the mismatch
// must be the node; otherwise the desired state moved, and the usual apply
// path runs regardless of drift policy. An empty recorded fingerprint (a
// machine's first reconcile) is never treated as drift, for the same
// reason: there is nothing to have converged against yet.
//
// One sharp edge of using the whole object's metadata.generation: it
// advances on *any* spec change, including TalosCluster.spec.driftPolicy
// itself, which does not feed the render at all. Flipping driftPolicy (or
// editing any other cluster field) on the same reconcile cycle as a
// pre-existing, unrelated node drift makes that one cycle look like a
// desired-state change instead, so it applies once instead of warning.
// Subsequent reconciles see a stable fingerprint again and behave
// correctly. This is a known coarseness of generation-based tracking, not a
// heuristic bolted on to paper over it.
package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/render"
)

// controllerIdentity is this component's stable logical name: the
// clusterlock.Acquire identity (clusterlock's own doc comment: it must be
// stable across restarts, not a pod name) and the naming convention's field
// manager for this component (cmd/main.go uses the same literal for the
// webhook cert rotator's FieldOwner).
const controllerIdentity = "ichor-controller"

// conditionReady and conditionConfigWarnings are the two condition types
// this reconciler writes to TalosMachine.status.conditions. Ready mirrors
// the CRD's own printcolumn; ConfigWarnings carries ValidateAtRuntime's
// dry-run warnings, separately, since they are informational and
// not always a reason the machine is un-Ready.
const (
	conditionReady          = "Ready"
	conditionConfigWarnings = "ConfigWarnings"
)

// Condition reasons. Every one of these is a terminal outcome for one
// reconcile: the next reconcile always starts over from TalosCluster,
// TalosConfigPatch and the node's live state (principle 7), never from
// these strings, so there is nothing else to resume beyond calling
// Reconcile again.
const (
	reasonClusterNotFound          = "ClusterNotFound"
	reasonSecretsBundleUnavailable = "SecretsBundleUnavailable"
	reasonRenderFailed             = "RenderFailed"
	reasonTalosClientNotConfigured = "TalosClientNotConfigured"
	reasonTalosConnectFailed       = "TalosConnectFailed"
	reasonCurrentConfigUnavailable = "CurrentConfigUnavailable"
	reasonConfigDiffFailed         = "ConfigDiffFailed"
	reasonDryRunFailed             = "DryRunFailed"
	reasonApplyFailed              = "ApplyFailed"
	reasonConverged                = "Converged"
	reasonApplied                  = "Applied"
	reasonUnsupportedTalosVersion  = "UnsupportedTalosVersion"
	reasonInstallTimeOnlyChange    = "InstallTimeOnlyChange"
	reasonRebootRequired           = "RebootRequired"
	reasonClusterLockHeld          = "ClusterLockHeld"
	reasonDryRunWarnings           = "DryRunWarnings"
	reasonNodeDrifted              = "NodeDrifted"
	reasonDriftCorrected           = "DriftCorrected"
	reasonClusterTransitioning     = "ClusterTransitioning"
)

// driftPolicyAutoCorrect is the one TalosCluster.spec.driftPolicy value that
// allows the reconciler to re-apply on drift. Anything else -- including the
// zero value, for a cluster the CRD default hasn't reached yet -- is
// treated as warnOnly, the safe side (design 5.6 step 5).
const driftPolicyAutoCorrect = "autoCorrect"

// TalosMachineReconciler reconciles a TalosMachine object.
type TalosMachineReconciler struct {
	client.Client

	// ControllerNamespace is the namespace loadBundle reads the secrets
	// bundle Secret from -- the controller's own namespace (POD_NAMESPACE in
	// cmd/main.go), not the TalosCluster's. Required: loadBundle
	// fails explicitly rather than guessing if this is empty.
	ControllerNamespace string

	// TalosClient opens the Talos API boundary for m, using cluster and the
	// already-loaded bundle (NewMachineryTalosClient is the real
	// implementation cmd/main.go wires in). nil means node connectivity
	// isn't wired up at all: Reconcile reports that plainly in status
	// (reasonTalosClientNotConfigured) instead of panicking -- the same is
	// true if bundle itself failed to load, since that's checked first and
	// this field is never reached in that case.
	//
	// There is only one credential this ever needs: design 5.6 only runs
	// against an approved machine that already has a configuration, i.e.
	// one that has already joined and already trusts the bundle's CA. The
	// insecure, unauthenticated path Talos's maintenance mode offers is
	// discovery's problem (phase 3), not this reconciler's.
	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	// KubeClient drains a control-plane TalosMachine's Node during removal
	// (removal.go). nil skips draining rather than failing the reconcile:
	// the quorum/last-control-plane/count checks that actually gate removal
	// never touch this field, so a cluster without it wired up still gets
	// the safety guarantee, just without the drain step.
	KubeClient kubernetes.Interface
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;delete
// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list

// Reconcile implements design 5.6's five numbered steps for one
// TalosMachine. A machine that isn't approved is left untouched entirely --
// no status write, nothing -- since rendering and applying configuration to
// it is exactly what approval authorizes.
func (r *TalosMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var m ichorv1alpha1.TalosMachine
	if err := r.Get(ctx, req.NamespacedName, &m); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !m.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.reconcileRemoval(ctx, &m)
	}

	if !m.Spec.IsApproved() {
		return ctrl.Result{}, nil
	}

	// Only an approved control plane needs removal.go's ordered removal
	// operation; a worker or an unapproved machine gets no
	// finalizer and deletes exactly as before.
	if m.Spec.Role == "controlplane" && !controllerutil.ContainsFinalizer(&m, machineRemovalFinalizer) {
		controllerutil.AddFinalizer(&m, machineRemovalFinalizer)
		return ctrl.Result{}, r.Update(ctx, &m)
	}

	var cluster ichorv1alpha1.TalosCluster

	clusterKey := types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonClusterNotFound,
				fmt.Sprintf("TalosCluster %q not found", clusterKey.Name))
		}

		return ctrl.Result{}, err
	}

	// Refreshed here, on every approved machine's reconcile, rather than on
	// its own schedule: architecture.md's "Nodes" wants the cluster's
	// Transitioning status kept current, not just recomputed when a
	// removal happens to run.
	if err := r.refreshTransitioning(ctx, &cluster, &m); err != nil {
		return ctrl.Result{}, err
	}

	// Step 1: load the bundle and gather this cluster's patches, then
	// render. Neither the bundle nor the rendered bytes are ever logged,
	// returned in an error string, or written to status below (principle
	// 3; pkg/render's own leak-test discipline).
	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, &cluster)
	if err != nil {
		log.Error(err, "loading secrets bundle")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonSecretsBundleUnavailable,
			"secrets bundle unavailable")
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := r.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	rendered, hash, err := render.Render(&cluster, &m, bundle, patches.Items)
	if err != nil {
		log.Error(err, "rendering machine configuration")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonRenderFailed,
			"rendering machine configuration failed")
	}

	// inputsHash fingerprints every object that just fed Render: this
	// TalosCluster, this TalosMachine, and whichever TalosConfigPatches
	// render.SelectPatches decided apply to it -- the same decision Render
	// itself just made, not a second guess at it. previousInputsHash is
	// whatever was recorded the last time the node was confirmed to match
	// what was rendered; empty on a machine's first reconcile, since there
	// is no previous convergence yet.
	inputsHash, err := desiredInputsHash(&cluster, &m, patches.Items)
	if err != nil {
		log.Error(err, "hashing render inputs")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonRenderFailed,
			"rendering machine configuration failed")
	}

	previousInputsHash := m.Status.ObservedInputsHash

	if r.TalosClient == nil {
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonTalosClientNotConfigured,
			"no Talos API connectivity is configured for this controller")
	}

	talos, err := r.TalosClient(ctx, &cluster, &m, bundle)
	if err != nil {
		log.Error(err, "connecting to Talos API")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonTalosConnectFailed,
			"connecting to the node's Talos API failed")
	}

	// TalosClient is three methods, no Close (the fake test double has
	// nothing to close); a real implementation's connection is closed here,
	// through the same optional-capability check client code uses for
	// io.Closer elsewhere, rather than growing the interface for it.
	if closer, ok := talos.(io.Closer); ok {
		defer closer.Close() //nolint:errcheck
	}

	// Step 2: read the node's current configuration and compare hashes.
	current, err := talos.CurrentConfig(ctx)
	if err != nil {
		log.Error(err, "reading current machine configuration")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonCurrentConfigUnavailable,
			"reading the node's current configuration failed")
	}

	if hashConfig(current) == hash {
		m.Status.AppliedConfigHash = hash
		m.Status.ObservedInputsHash = inputsHash

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionTrue, reasonConverged,
			"node configuration matches the rendered configuration")
	}

	// drifted is true only when nothing that feeds the render has changed
	// since Ichor last confirmed the node matched it: the mismatch just
	// found above then has only one explanation left, the node moved on its
	// own (design 5.6 step 5). previousInputsHash is empty on a machine's
	// first reconcile, which is deliberately not drift -- there is no prior
	// convergence to compare against, so a first-time mismatch is treated
	// the same as any other desired-state change, below.
	drifted := previousInputsHash != "" && previousInputsHash == inputsHash

	// Step 3: work out which field paths changed and what that implies.
	paths, err := ChangedPaths(current, rendered)
	if err != nil {
		log.Error(err, "diffing current and rendered configuration")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonConfigDiffFailed,
			"comparing current and rendered configuration failed")
	}

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonRenderFailed,
			"invalid versionContract")
	}

	impact := fieldmeta.Assess(paths, contract)

	// Step 4: dry run first, always -- side-effect free, and its warnings
	// are worth surfacing regardless of which way step 5 goes.
	// Mode/ModeDetails on the response are never read: AUTO is a hardcoded
	// alias for NO_REBOOT and never inspects the diff.
	warnings, err := talos.DryRunApply(ctx, rendered)
	if err != nil {
		log.Error(err, "dry-run applying rendered configuration")

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonDryRunFailed,
			"dry-run apply failed")
	}

	r.setWarnings(&m, warnings)

	// Step 5: apply, refuse, or stop, depending on what step 3 found -- and,
	// now, on whether this is drift. A reboot-requiring, install-time-only
	// or unsupported change is always just reported, never applied,
	// regardless of drift or driftPolicy: those paths already do the right
	// thing, drift only changes what their message says. The one new
	// outcome is a safe drift left alone because driftPolicy is warnOnly
	// (the default): design 5.6 step 5 wants drift visible, not silently
	// corrected.
	driftNote := ""
	if drifted {
		driftNote = "node configuration has drifted from the rendered configuration; "
	}

	switch {
	case len(impact.Unsupported) > 0:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonUnsupportedTalosVersion,
			fmt.Sprintf("%sneeds a newer Talos version than the pinned contract %s: %s",
				driftNote, cluster.Spec.VersionContract, strings.Join(impact.Unsupported, ", ")))

	case len(impact.InstallTimeOnly) > 0:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonInstallTimeOnlyChange,
			fmt.Sprintf("%sneeds a reset and reinstall, not an apply (install-time-only fields changed: %s)",
				driftNote, strings.Join(impact.InstallTimeOnly, ", ")))

	case impact.RebootRequired:
		return ctrl.Result{}, r.refuseReboot(ctx, &m, &cluster, driftNote)

	case drifted && cluster.Spec.DriftPolicy != driftPolicyAutoCorrect:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonNodeDrifted,
			fmt.Sprintf("%sdriftPolicy is warnOnly, so it was left unchanged (changed paths: %s)",
				driftNote, strings.Join(paths, ", ")))

	default:
		if err := talos.ApplyNoReboot(ctx, rendered); err != nil {
			log.Error(err, "applying rendered configuration")

			return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonApplyFailed,
				"applying rendered configuration failed")
		}

		m.Status.AppliedConfigHash = hash
		m.Status.ObservedInputsHash = inputsHash

		reason, message := reasonApplied, "applied configuration without a reboot"
		if drifted {
			reason = reasonDriftCorrected
			message = "node configuration had drifted; re-applied because driftPolicy is autoCorrect " +
				"and every changed path is reboot-free"
		}

		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionTrue, reason, message)
	}
}

// desiredInputsHash fingerprints the generation of every object that fed
// this reconcile's render: cluster, m, and whichever of candidates
// render.SelectPatches decides apply to m -- the same question Render
// itself just answered, asked again rather than guessed at separately, so
// a patch unrelated to m never perturbs the fingerprint. The result is a
// hash only (principle 3): generations are not secret, but a plain join of
// them is unnecessary detail no caller needs to parse back out.
func desiredInputsHash(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
) (string, error) {
	applicable, err := render.SelectPatches(cluster, m, candidates)
	if err != nil {
		return "", fmt.Errorf("selecting applicable patches: %w", err)
	}

	parts := make([]string, 0, len(applicable)+2)
	parts = append(parts, fmt.Sprintf("cluster=%d", cluster.Generation), fmt.Sprintf("machine=%d", m.Generation))

	for _, p := range applicable {
		parts = append(parts, fmt.Sprintf("patch:%s=%d", p.Name, p.Generation))
	}

	return render.Hash([]byte(strings.Join(parts, ";"))), nil
}

// refuseReboot implements the reboot-needed branch of design step 5: take
// the cluster lock, then stop. Draining, the node-operation hook and the
// etcd checks that must precede a real reboot are phase 3 and 7 work (unit
// rule 4); applying or rebooting without them would take workloads down, so
// the only correct move here is a visible, lock-protected refusal.
func (r *TalosMachineReconciler) refuseReboot(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, cluster *ichorv1alpha1.TalosCluster, driftNote string,
) error {
	log := logf.FromContext(ctx)

	// architecture.md's "Nodes": while the cluster has an even number of
	// approved control planes (a replacement in progress, 3->4->3), the
	// cluster lock is supposed to block other disruptive work -- but the
	// lock itself is only held for the brief window around each individual
	// add or remove, not for the whole human-paced transition between
	// them. Recomputed fresh rather than trusting TalosCluster.status's
	// own Transitioning condition, which could be one reconcile stale.
	if controlPlanes, err := r.countApprovedControlPlanes(ctx, m); err != nil {
		return err
	} else if transitioningControlPlanes(controlPlanes) {
		return r.ready(ctx, m, metav1.ConditionFalse, reasonClusterTransitioning,
			fmt.Sprintf("%scluster has %d approved control planes (even -- a replacement is in progress); "+
				"other disruptive work is refused until it returns to the declared odd count",
				driftNote, controlPlanes))
	}

	lock := clusterlock.New(r.Client, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)

	if err := lock.Acquire(ctx, controllerIdentity); err != nil {
		if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
			return r.ready(ctx, m, metav1.ConditionFalse, reasonClusterLockHeld,
				fmt.Sprintf("cluster lock held by %q; reboot-requiring change deferred", held.Holder))
		}

		return err
	}

	defer func() {
		if err := lock.Release(ctx, controllerIdentity); err != nil {
			log.Error(err, "releasing cluster lock")
		}
	}()

	return r.ready(ctx, m, metav1.ConditionFalse, reasonRebootRequired,
		driftNote+"configuration change needs a reboot; this phase does not reboot nodes -- "+
			"draining, the node-operation hook and etcd checks that must precede a reboot are later work")
}

// ready sets the Ready condition, keeps observedGeneration honest
// (principle 7: every reconcile attempt, successful or not, is recorded),
// and persists status in one write.
func (r *TalosMachineReconciler) ready(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, status metav1.ConditionStatus, reason, message string,
) error {
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionReady, Status: status, Reason: reason, Message: message,
	})
	m.Status.ObservedGeneration = m.Generation

	return r.Status().Update(ctx, m)
}

// setWarnings records dry-run's warnings as the ConfigWarnings condition,
// mutating m in place; the caller persists it together with the Ready
// condition in the same status write. It is only called once a dry run has
// actually happened this reconcile, so a reconcile that fails before that
// point leaves a previous ConfigWarnings condition alone rather than
// erasing it.
func (r *TalosMachineReconciler) setWarnings(m *ichorv1alpha1.TalosMachine, warnings []string) {
	status := metav1.ConditionFalse
	message := "dry run reported no warnings"

	if len(warnings) > 0 {
		status = metav1.ConditionTrue
		message = strings.Join(warnings, "; ")
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigWarnings, Status: status, Reason: reasonDryRunWarnings, Message: message,
	})
}

// hashConfig hashes cfg the same way pkg/render.Render hashes its output,
// so the two are comparable.
func hashConfig(cfg []byte) string {
	return render.Hash(cfg)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosMachine{}).
		Named("talosmachine").
		Complete(r)
}
