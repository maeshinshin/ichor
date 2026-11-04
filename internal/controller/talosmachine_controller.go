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
	"slices"
	"strconv"
	"strings"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/render"
)

// controllerIdentity is this component's stable logical name: the
// clusterlock.Acquire identity (clusterlock's own doc comment: it must be
// stable across restarts, not a pod name) and the naming convention's field
// manager for this component (cmd/main.go uses the same literal for the
// webhook cert rotator's FieldOwner).
const controllerIdentity = "ichor-controller"

// roleControlPlane is TalosMachineSpec.Role's control-plane value, shared
// across this package's own uses of it (talosoperation_controller.go and
// removal.go) rather than each repeating the literal.
const roleControlPlane = "controlplane"

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
	reasonCNIUnsupported           = "CNIUnsupported"
	reasonTalosClientNotConfigured = "TalosClientNotConfigured"
	reasonTalosConnectFailed       = "TalosConnectFailed"
	reasonCurrentConfigUnavailable = "CurrentConfigUnavailable"
	reasonEndpointIdentityMismatch = "EndpointIdentityMismatch"
	reasonConfigDiffFailed         = "ConfigDiffFailed"
	reasonDryRunFailed             = "DryRunFailed"
	reasonApplyFailed              = "ApplyFailed"
	reasonConverged                = "Converged"
	reasonApplied                  = "Applied"
	reasonUnsupportedTalosVersion  = "UnsupportedTalosVersion"
	reasonInstallTimeOnlyChange    = "InstallTimeOnlyChange"
	reasonRebootRequired           = "RebootRequired"
	reasonAPIServerRestartRequired = "APIServerRestartRequired"
	reasonKubeProxySwitchPending   = "KubeProxySwitchPending"
	reasonClusterLockHeld          = "ClusterLockHeld"
	reasonDryRunWarnings           = "DryRunWarnings"
	reasonNodeDrifted              = "NodeDrifted"
	reasonDriftCorrected           = "DriftCorrected"
	reasonClusterTransitioning     = "ClusterTransitioning"
	reasonCARotationInProgress     = "CARotationInProgress"
	reasonControlPlaneOverCap      = "ControlPlaneOverCap"
	reasonAdoptionHeld             = "AdoptionHeld"

	// conditionAdoptionHeld is True while TalosMachineHoldApplyAnnotation is set.
	conditionAdoptionHeld = "AdoptionHeld"
)

// ensureRemovalFinalizer gives an approved control plane machineRemovalFinalizer and reports true
// when it wrote one, so the caller returns and the update's event re-enters Reconcile.
func (r *TalosMachineReconciler) ensureRemovalFinalizer(ctx context.Context, m *ichorv1alpha1.TalosMachine) (bool, error) {
	if m.Spec.Role != roleControlPlane || controllerutil.ContainsFinalizer(m, machineRemovalFinalizer) {
		return false, nil
	}

	controllerutil.AddFinalizer(m, machineRemovalFinalizer)

	return true, r.Update(ctx, m)
}

// refuseUnsupportedCNI sets Ready=False/CNIUnsupported on m and reports stop=true when the cluster's
// preset is not cilium. Nothing is rendered and nothing on the node is touched.
func (r *TalosMachineReconciler) refuseUnsupportedCNI(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (ctrl.Result, bool, error) {
	p := cluster.Spec.CNI.Preset
	if p == "" || p == ciliumName || p == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		return ctrl.Result{}, false, nil
	}

	res, err := r.waitForChange(ctx, m, reasonCNIUnsupported,
		fmt.Sprintf("spec.cni.preset %q was removed; delete and recreate the cluster with cilium", p))

	return res, true, err
}

// holdIfOverCap sets Ready=False/ControlPlaneOverCap on m and reports stop=true when m is a control plane
// beyond spec.controlPlane.count+1 (deleteguard.ControlPlaneOverCap); the caller then stops before
// render and apply. Nothing on the node is touched, even if m is already an etcd member.
func (r *TalosMachineReconciler) holdIfOverCap(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (bool, error) {
	if m.Spec.Role != roleControlPlane {
		return false, nil
	}

	held, total, err := deleteguard.ControlPlaneOverCap(
		ctx, r.APIReader, m.Namespace, m.Spec.ClusterRef.Name, m.Name, int(cluster.Spec.ControlPlane.Count))
	if err != nil {
		return true, err
	}

	if !held {
		return false, nil
	}

	// No poll: a sibling control plane appearing or leaving re-enqueues this machine (controlPlaneSiblings).
	return true, r.ready(ctx, m, metav1.ConditionFalse, reasonControlPlaneOverCap,
		fmt.Sprintf("%d control planes are approved against spec.controlPlane.count %d (at most count+1 is allowed); "+
			"this is among the newest approvals, so no configuration is applied until another control plane "+
			"is removed or the count is raised", total, cluster.Spec.ControlPlane.Count))
}

// transientRecheck is how often a machine whose Ready=False has a cause that time can fix (node
// unreachable, Secret not yet created, lock held ...) looks again.
const transientRecheck = 30 * time.Second

// reachabilityRecheck is how often a converged machine is probed again. Nothing else would notice
// a node that went away or moved: no event reaches a controller that is not watching the node, so
// without it Ready stays True for a machine nobody can reach.
const reachabilityRecheck = 2 * time.Minute

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
	// This is the authenticated credential: it works once the node trusts
	// the bundle's CA. A node still in maintenance mode does not, and is
	// provisioned through MaintenanceClient below (talosmachine_provision.go).
	TalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (TalosClient, error)

	// MaintenanceClient opens the insecure maintenance-mode connection used
	// to give an approved machine its first configuration
	// (NewMachineryMaintenanceClient is the real implementation). nil turns
	// provisioning off: a machine the authenticated client cannot reach then
	// just reports CurrentConfigUnavailable.
	MaintenanceClient func(ctx context.Context, endpoint string) (MaintenanceClient, error)

	// KubeClient drains a control-plane TalosMachine's Node during removal
	// (removal.go). nil skips draining rather than failing the reconcile:
	// the quorum/last-control-plane/count checks that actually gate removal
	// never touch this field, so a cluster without it wired up still gets
	// the safety guarantee, just without the drain step.
	KubeClient kubernetes.Interface

	// Evacuator moves storage replicas off a control plane's node before
	// removal.go drains it (the same StorageEvacuator the Talos upgrade
	// uses). nil skips evacuation: a cluster with no storage system has
	// nothing to coordinate.
	Evacuator StorageEvacuator

	// APIReader counts approved control planes for the quorum checks in
	// removal.go, bypassing the manager's cache that Client (above) reads
	// through everywhere else. A cached read can lag the deletion of a
	// control plane that just committed, drifting the count high -- the
	// dangerous direction here, since it is what lets a second, concurrent
	// deletion look safe when it is not (not
	// the usual environment-noise direction). Wired from
	// mgr.GetAPIReader() in cmd/main.go.
	APIReader client.Reader

	// Recorder emits the Event removal.go's completeRemoval raises in the
	// same breath as clearing machineRemovalFinalizer -- the only record
	// that survives the TalosMachine's own disappearance to tell an
	// operator its physical node still holds this cluster's PKI. nil skips the Event, not the removal itself.
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",namespace=ichor-system,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list
// Deleting a pod is a Role in kube-system only (config/rbac/pod_restart_role.yaml); a drain evicts.
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
		return r.reconcileDeletion(ctx, &m)
	}

	if !m.Spec.IsApproved() {
		return ctrl.Result{}, nil
	}

	// Only an approved control plane needs removal.go's ordered removal
	// operation; a worker or an unapproved machine gets no
	// finalizer and deletes exactly as before.
	if added, err := r.ensureRemovalFinalizer(ctx, &m); added {
		return ctrl.Result{}, err
	}

	var cluster ichorv1alpha1.TalosCluster

	clusterKey := types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}
	if err := r.Get(ctx, clusterKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			// The TalosCluster watch re-enqueues this machine when the cluster is created.
			return r.waitForChange(ctx, &m, reasonClusterNotFound,
				fmt.Sprintf("TalosCluster %q not found", clusterKey.Name))
		}

		return ctrl.Result{}, err
	}

	// A preset other than cilium is not rendered: rendering would strip Talos's flannel
	// and take that cluster's network down. Nothing on the node is touched.
	// Refreshed here, on every approved machine's reconcile, rather than on
	// its own schedule: architecture.md's "Nodes" wants the cluster's
	// Transitioning status kept current, not just recomputed when a
	// removal happens to run.
	if err := r.refreshTransitioning(ctx, &cluster, &m); err != nil {
		return ctrl.Result{}, err
	}

	// Gates that keep the machine from being rendered or applied this round.
	if res, stop, err := r.holdUntilEligible(ctx, &cluster, &m); stop {
		return res, err
	}

	// Step 1: load the bundle and gather this cluster's patches, then
	// render. Neither the bundle nor the rendered bytes are ever logged,
	// returned in an error string, or written to status below (principle
	// 3; pkg/render's own leak-test discipline).
	bundle, err := loadBundle(ctx, r.Client, r.ControllerNamespace, &cluster)
	if err != nil {
		log.Error(err, "loading secrets bundle")

		// The Secret watch (machinesOfAuthSecret) re-enqueues this machine when the bundle appears.
		return r.waitForChange(ctx, &m, reasonSecretsBundleUnavailable,
			"secrets bundle unavailable")
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := r.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	// The system layer's inputs (registry credentials, TalosStorages), read once so that the
	// render and the fingerprint below see the same TalosStorages.
	rendered, hash, sys, err := r.renderMachine(ctx, &cluster, &m, bundle, patches.Items)
	if err != nil {
		log.Error(err, "rendering machine configuration")

		// Every render input is watched, so a correction re-enqueues this machine.
		return r.waitForChange(ctx, &m, reasonRenderFailed,
			"rendering machine configuration failed")
	}

	// inputsHash fingerprints every object that just fed Render: this
	// TalosCluster, this TalosMachine, and whichever TalosConfigPatches
	// render.SelectPatches decided apply to it -- the same decision Render
	// itself just made, not a second guess at it. previousInputsHash is
	// whatever was recorded the last time the node was confirmed to match
	// what was rendered; empty on a machine's first reconcile, since there
	// is no previous convergence yet.
	inputsHash, err := desiredInputsHash(&cluster, &m, patches.Items, sys.Storage, registryAuthFingerprint(sys.RegistryAuth))
	if err != nil {
		log.Error(err, "hashing render inputs")

		// Every render input is watched, so a correction re-enqueues this machine.
		return r.waitForChange(ctx, &m, reasonRenderFailed,
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

		return r.retryLater(ctx, &m, reasonTalosConnectFailed,
			"connecting to the node's Talos API failed")
	}

	defer closeTalosClient(talos)

	// Step 2: read the node's current configuration and compare hashes.
	current, res, stop, err := r.readNode(ctx, &cluster, &m, talos, rendered, hash)
	if stop {
		return res, err
	}

	// Step 3: work out which field paths changed and what that implies. No path means the node
	// already has the rendered configuration, in the same bytes or in another form (legacy mirrors).
	paths, err := changedPaths(current, rendered, hash)
	if err != nil {
		log.Error(err, "diffing current and rendered configuration")

		return r.retryLater(ctx, &m, reasonConfigDiffFailed,
			"comparing current and rendered configuration failed")
	}

	if len(paths) == 0 {
		m.Status.AppliedConfigHash = hash
		m.Status.ObservedInputsHash = inputsHash
		m.Status.RebootPaths, m.Status.APIServerRestartPaths = nil, nil

		return ctrl.Result{RequeueAfter: reachabilityRecheck}, r.ready(ctx, &m, metav1.ConditionTrue, reasonConverged,
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

	contract, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return r.waitForChange(ctx, &m, reasonRenderFailed,
			"invalid versionContract")
	}

	impact := assessChange(ctx, talos, m.Spec.Hostname, paths, contract)

	// Step 4: dry run first, always -- side-effect free, and its warnings
	// are worth surfacing regardless of which way step 5 goes.
	// Mode/ModeDetails on the response are never read: AUTO is a hardcoded
	// alias for NO_REBOOT and never inspects the diff.
	warnings, err := talos.DryRunApply(ctx, rendered)
	if err != nil {
		log.Error(err, "dry-run applying rendered configuration")

		return r.retryLater(ctx, &m, reasonDryRunFailed,
			"dry-run apply failed")
	}

	r.setWarnings(&m, bundle, warnings)

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

	m.Status.RebootPaths, m.Status.APIServerRestartPaths = nil, nil

	switch {
	case len(impact.Unsupported) > 0:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonUnsupportedTalosVersion,
			fmt.Sprintf("%sneeds a newer Talos version than the pinned contract %s: %s",
				driftNote, cluster.Spec.VersionContract, strings.Join(impact.Unsupported, ", ")))

	case len(impact.InstallTimeOnly) > 0:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonInstallTimeOnlyChange,
			fmt.Sprintf("%sneeds a reset and reinstall, not an apply (install-time-only fields changed: %s)",
				driftNote, strings.Join(impact.InstallTimeOnly, ", ")))

	case kubeProxySwitchOnly(&cluster, &m, impact):
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonKubeProxySwitchPending,
			driftNote+"spec.cni.kubeProxy changed to "+cluster.Spec.CNI.KubeProxy+"; run `ichor cni kube-proxy "+
				cluster.Spec.CNI.KubeProxy+"` (or the UI's kube-proxy switch) to apply it without a reboot")

	case needsOperation(&m, impact):
		return r.refuseForOperation(ctx, &m, driftNote, current, rendered, impact)

	case drifted && cluster.Spec.DriftPolicy != driftPolicyAutoCorrect:
		return ctrl.Result{}, r.ready(ctx, &m, metav1.ConditionFalse, reasonNodeDrifted,
			fmt.Sprintf("%sdriftPolicy is warnOnly, so it was left unchanged (changed paths: %s)",
				driftNote, strings.Join(paths, ", ")))

	default:
		log.Info("applying the rendered configuration without a reboot", "machine", m.Name, "paths", paths, "drift", drifted)

		if err := talos.ApplyNoReboot(ctx, rendered); err != nil {
			log.Error(err, "applying rendered configuration")

			return r.retryLater(ctx, &m, reasonApplyFailed,
				"applying rendered configuration failed")
		}

		log.Info("applied the rendered configuration", "machine", m.Name, "configHash", hash)

		m.Status.AppliedConfigHash = hash
		m.Status.ObservedInputsHash = inputsHash

		reason, message := reasonApplied, "applied configuration without a reboot"
		if drifted {
			reason = reasonDriftCorrected
			message = "node configuration had drifted; re-applied because driftPolicy is autoCorrect " +
				"and every changed path is reboot-free"
		}

		return ctrl.Result{RequeueAfter: reachabilityRecheck}, r.ready(ctx, &m, metav1.ConditionTrue, reason, message)
	}
}

// readNode reads the node's current configuration with the bundle's identity (step 2). stop=true
// means the reconcile is over for this round, with res and err as the result: the node could not be
// read, was provisioned or is still being followed to running (talosmachine_provision.go).
func (r *TalosMachineReconciler) readNode(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	talos TalosClient, rendered []byte, hash string,
) (current []byte, res ctrl.Result, stop bool, err error) {
	readCtx, cancelRead := context.WithTimeout(ctx, talosCallTimeout)
	current, err = talos.CurrentConfig(readCtx)

	cancelRead()

	if err != nil {
		// A joined node that moved (a new lease, a recreated pod) is followed to where the
		// Kubernetes Node says it is; the next round dials the new address.
		if moved, ferr := r.followNodeAddress(ctx, m); ferr != nil {
			logf.FromContext(ctx).V(1).Info("following the node's address failed", "error", ferr.Error())
		} else if moved {
			return nil, ctrl.Result{RequeueAfter: time.Second}, true, nil
		}

		// A machine that has never converged and cannot be read with the bundle's identity may
		// still be in maintenance mode: provision finds out by asking the insecure API, not by
		// interpreting this error.
		if r.MaintenanceClient != nil && m.Status.AppliedConfigHash == "" {
			// Expected while the node is in maintenance mode: not an error, no stack.
			logf.FromContext(ctx).V(1).Info("authenticated read failed on a never-converged machine; trying the maintenance API",
				"reason", err.Error())

			res, err = r.provision(ctx, cluster, m, rendered, hash)

			return nil, res, true, err
		}

		logf.FromContext(ctx).Error(err, "reading current machine configuration")

		res, err = r.readFailed(ctx, m, err, time.Now())

		return nil, res, true, err
	}

	if res, stop, err = r.checkEndpointIdentity(ctx, m, talos); stop {
		return nil, res, true, err
	}

	apimeta.RemoveStatusCondition(&m.Status.Conditions, conditionNodeReachable)

	schemCtx, cancelSchem := context.WithTimeout(ctx, talosCallTimeout)
	if schematic, serr := talos.Schematic(schemCtx); serr == nil {
		m.Status.Schematic = schematic
	}

	cancelSchem()

	recordSecurity(ctx, cluster, m, talos)

	// Recorded once: a node does not rename itself, and asking again would be a call per reconcile.
	if m.Status.NodeName == "" {
		nameCtx, cancelName := context.WithTimeout(ctx, talosCallTimeout)
		if name, nerr := talos.NodeName(nameCtx); nerr == nil {
			m.Status.NodeName = name
		}

		cancelName()
	}

	// A machine that was reset and approved again comes back to its old Node, still cordoned by the drain.
	if uerr := r.uncordonRejoinedNode(ctx, m.Status.NodeName); uerr != nil {
		logf.FromContext(ctx).Error(uerr, "uncordoning the rejoined node")
	}

	res, stop, err = r.followStage(ctx, talos, m)
	if stop {
		return current, res, stop, err
	}

	res, stop, err = r.reportHold(ctx, m, current, rendered, hash)

	return current, res, stop, err
}

// holdUntilEligible runs the checks that stop m from being rendered or applied this round, before
// anything is read or dialed. stop=true also on an error, as holdIfOverCap does.
//
// The webhook's cap can be raced past, so recount here and hold the newest approvals
// beyond count+1.
//
// A CA rotation moves every node through stages the secrets bundle does not describe --
// the bundle names the old CAs while the nodes already trust the new ones -- so rendering from it,
// dialing with it or applying it would fight the rotation and could undo a stage. The machine is
// left alone until the rotation is done. Read uncached: it gates an apply.
func (r *TalosMachineReconciler) holdUntilEligible(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (res ctrl.Result, stop bool, err error) {
	if res, stop, err := r.refuseUnsupportedCNI(ctx, cluster, m); stop {
		return res, true, err
	}

	if stop, err := r.holdIfOverCap(ctx, cluster, m); stop {
		return ctrl.Result{}, true, err
	}

	rotation, err := activeCARotation(ctx, readerOrClient(r.APIReader, r.Client), m.Namespace, cluster.Name)
	if err != nil {
		return ctrl.Result{}, true, err
	}

	if rotation != "" {
		return ctrl.Result{RequeueAfter: transientRecheck}, true, r.ready(ctx, m, metav1.ConditionUnknown, reasonCARotationInProgress,
			fmt.Sprintf("TalosOperation %q is rotating the cluster's CAs; this machine is not reconciled until it completes", rotation))
	}

	// A key rotation puts configuration on the control planes that the bundle does not describe either.
	rotation, err = activeKeyRotation(ctx, readerOrClient(r.APIReader, r.Client), m.Namespace, cluster.Name)
	if err != nil || rotation == "" {
		return ctrl.Result{}, err != nil, err
	}

	return ctrl.Result{RequeueAfter: transientRecheck}, true, r.ready(ctx, m, metav1.ConditionUnknown, reasonKeyRotationInProgress,
		fmt.Sprintf("TalosOperation %q is rotating the cluster's service-account and secretbox keys; this machine is not reconciled until it completes", rotation))
}

// renderVersion is render.Version; a variable only so tests can simulate an Ichor upgrade.
var renderVersion = render.Version

// desiredInputsHash fingerprints the generation of every object that fed
// this reconcile's render: cluster, m, and whichever of candidates
// render.SelectPatches decides apply to m -- the same question Render
// itself just answered, asked again rather than guessed at separately, so
// a patch unrelated to m never perturbs the fingerprint. The result is a
// hash only (principle 3): generations are not secret, but a plain join of
// them is unnecessary detail no caller needs to parse back out.
func desiredInputsHash(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
	storages []ichorv1alpha1.TalosStorage, registryAuth string,
) (string, error) {
	applicable, err := render.SelectPatches(cluster, m, candidates)
	if err != nil {
		return "", fmt.Errorf("selecting applicable patches: %w", err)
	}

	parts := make([]string, 0, len(applicable)+2)
	parts = append(parts, fmt.Sprintf("render=%d", renderVersion),
		fmt.Sprintf("cluster=%d", cluster.Generation), fmt.Sprintf("machine=%d", m.Generation))

	for _, p := range applicable {
		parts = append(parts, fmt.Sprintf("patch:%s=%d", p.Name, p.Generation))
	}

	// The TalosStorage governing m feeds the render too; a change to it must read as
	// a desired-state change, not as drift.
	chosen, err := render.StorageFor(cluster, m, storages)
	if err != nil {
		return "", fmt.Errorf("selecting governing TalosStorage: %w", err)
	}

	if chosen != nil {
		parts = append(parts, fmt.Sprintf("storage:%s=%d", chosen.Name, chosen.Generation))
	}

	// The registry credentials feed the render too: a changed password reads as a
	// desired-state change. registryAuth is registryAuthFingerprint's hash, never the data; empty
	// when no registry names a Secret, which leaves the hash as it was.
	if registryAuth != "" {
		parts = append(parts, "registryauth="+registryAuth)
	}

	// Two TalosCluster.status values feed the render as well: the CNI
	// hand-over drops the CNI's inlineManifests entry, and status.cni.kubeProxy overrides the
	// kube-proxy mode. metadata.generation does not move for a status write, so without these
	// the render change they cause would read as the node drifting.
	if apimeta.IsStatusConditionTrue(cluster.Status.Conditions, render.ConditionCNIHandover) {
		parts = append(parts, "cnihandover=true")
	}

	if mode, fromStatus := render.KubeProxyMode(cluster); fromStatus {
		parts = append(parts, "kubeproxy="+string(mode))
	}

	// The latch that adds the apiserver's --kubelet-certificate-authority is a status value too.
	if render.KubeletServingVerified(cluster) {
		parts = append(parts, "kubeletservingverified=true")
	}

	return render.Hash([]byte(strings.Join(parts, ";"))), nil
}

// registryAuthFingerprint hashes the resolved registry credentials (principle 3: the result is a
// hash, never the data) so that desiredInputsHash moves when a referenced Secret's password,
// auth or identityToken changes. It hashes the three values the render uses rather than the
// Secret's resourceVersion, so a label or annotation edit on the Secret does not read as a
// desired-state change. Empty for no credentials.
func registryAuthFingerprint(auth map[string]render.RegistryAuth) string {
	if len(auth) == 0 {
		return ""
	}

	hosts := make([]string, 0, len(auth))
	for h := range auth {
		hosts = append(hosts, h)
	}

	slices.Sort(hosts)

	var b strings.Builder

	for _, h := range hosts {
		a := auth[h]
		// %q quotes each field, so a value cannot run into its neighbour.
		fmt.Fprintf(&b, "%q:%q,%q,%q;", h, a.Password, a.Auth, a.IdentityToken)
	}

	return render.Hash([]byte(b.String()))
}

// isKubeProxyPath is a field the kube-proxy switch owns.
func isKubeProxyPath(p string) bool {
	return strings.HasPrefix(p, "KubeProxyConfig") || strings.HasPrefix(p, "cluster.proxy")
}

// kubeProxySwitchPending is true while the consent annotation names the spec's kube-proxy mode and
// status.cni.kubeProxy does not yet: the spec was changed ahead of the switch (Git first), in either
// direction, and only the cni operation may apply it. The annotation is the signal because spec alone
// cannot tell a switch from a patch-made cluster.proxy.disabled change, which has no annotation and
// stays an ordinary reboot-required change.
func kubeProxySwitchPending(cluster *ichorv1alpha1.TalosCluster) bool {
	want := cluster.Spec.CNI.KubeProxy
	if cluster.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation] != want {
		return false
	}

	return cluster.Status.CNI == nil || cluster.Status.CNI.KubeProxy != want
}

// kubeProxySwitchOnly reports a reboot-class change made only of kube-proxy fields (and no API server
// restart on a control plane) while a switch is pending. It is not a reboot to schedule: a config
// rollout would reboot the node and apply the mode without the switch's checks.
func kubeProxySwitchOnly(cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, impact fieldmeta.Impact) bool {
	if !kubeProxySwitchPending(cluster) || !impact.RebootRequired ||
		(len(impact.APIServerRestartPaths) > 0 && m.Spec.Role == roleControlPlane) {
		return false
	}

	return slices.IndexFunc(impact.RebootPaths, func(p string) bool { return !isKubeProxyPath(p) }) < 0
}

// needsOperation reports whether the change is left to a config operation: it needs a reboot, or
// it restarts the API server of a control plane (a worker has none and is applied like a safe change).
func needsOperation(m *ichorv1alpha1.TalosMachine, impact fieldmeta.Impact) bool {
	return impact.RebootRequired || (len(impact.APIServerRestartPaths) > 0 && m.Spec.Role == roleControlPlane)
}

// refuseForOperation reports a change needsOperation holds back; the API server restart fields
// ride along in status when a reboot is needed.
//
// Reboot form: this reconciler never reboots a node and never takes the cluster lock here: the
// hook, the drain and the etcd checks that must precede a reboot belong to a config-rollout
// TalosOperation, which someone has to start and which takes the lock itself. Here it
// only records status.rebootPaths (the rollout's plan reads them) and reports
// Ready=False/RebootRequired, naming the fields.
//
// Restart form: a change that needs no reboot but restarts this node's kube-apiserver. Applying it
// from here would restart every control plane's API server at about the same time, a full API
// outage on an HA cluster, so it is left to a config-rollout TalosOperation,
// which does it one control plane at a time and waits for /readyz between them.
func (r *TalosMachineReconciler) refuseForOperation(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, driftNote string, current, rendered []byte, impact fieldmeta.Impact,
) (ctrl.Result, error) {
	paths := reportablePaths(current, rendered, impact.APIServerRestartPaths)

	if impact.RebootRequired {
		m.Status.APIServerRestartPaths = paths
		m.Status.RebootPaths = reportablePaths(current, rendered, impact.RebootPaths)

		return ctrl.Result{}, r.ready(ctx, m, metav1.ConditionFalse, reasonRebootRequired,
			driftNote+"configuration change needs a reboot (fields: "+summarizePaths(m.Status.RebootPaths, rebootPathsInMessage)+
				")"+renameNote(m.Status.RebootPaths)+"; nothing reboots a node on its own -- run `ichor config apply --cluster "+m.Spec.ClusterRef.Name+"` (or start a config rollout on the cluster page) to apply it")
	}

	m.Status.APIServerRestartPaths = paths

	return ctrl.Result{}, r.ready(ctx, m, metav1.ConditionFalse, reasonAPIServerRestartRequired,
		driftNote+"configuration change restarts the Kubernetes API server (fields: "+summarizePaths(paths, rebootPathsInMessage)+
			"); no reboot, but it is not applied on its own, so every control plane does not lose its API server at once -- "+
			"run `ichor config apply --cluster "+m.Spec.ClusterRef.Name+" --follow` (or start a config rollout on the cluster page) to apply it one control plane at a time")
}

// rebootPathsInMessage caps how many field paths a Ready message lists; status.rebootPaths has all.
const rebootPathsInMessage = 5

// summarizePaths joins up to limit paths and counts the rest.
func summarizePaths(paths []string, limit int) string {
	if len(paths) <= limit {
		return strings.Join(paths, ", ")
	}

	return fmt.Sprintf("%s, and %d more", strings.Join(paths[:limit], ", "), len(paths)-limit)
}

// lockRefusal turns an acquireDisruptiveLock error into a visible, retried refusal; deferred
// names what is being put off ("reboot-requiring change deferred"). The transition ending or the
// lock being released enqueues nothing, so all of these poll. Any other error is returned as is.
func (r *TalosMachineReconciler) lockRefusal(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, err error, note, deferred string,
) (ctrl.Result, error) {
	if shuttingDown, ok := errors.AsType[*shuttingDownError](err); ok {
		return r.retryLater(ctx, m, shuttingDown.reason(), fmt.Sprintf("%s%s; %s", note, shuttingDown.Error(), deferred))
	}

	if transitioning, ok := errors.AsType[*transitioningError](err); ok {
		return r.retryLater(ctx, m, reasonClusterTransitioning,
			fmt.Sprintf("%s%s; other disruptive work is refused until it returns to the declared odd count",
				note, transitioning.Error()))
	}

	if held, ok := errors.AsType[*clusterlock.HeldError](err); ok {
		return r.retryLater(ctx, m, reasonClusterLockHeld, fmt.Sprintf("%s; %s", lockHeldBy(held), deferred))
	}

	return ctrl.Result{}, err
}

// retryLater records a False Ready for a failure that time can fix and asks to be reconciled again
// after transientRecheck. It is a Result, not an error: the status written here is the report (an
// error would add a stack to the log and the exponential backoff, up to ~17 minutes, would stretch
// recovery after a whole-cluster restart). If the status write itself fails, that error wins and
// controller-runtime backs off, as for any other.
func (r *TalosMachineReconciler) retryLater(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, reason, message string,
) (ctrl.Result, error) {
	if err := r.ready(ctx, m, metav1.ConditionFalse, reason, message); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: transientRecheck}, nil
}

// waitForChange records a False Ready for a failure only a change to a watched object can fix, and
// does not requeue: the watches in SetupWithManager deliver that change.
func (r *TalosMachineReconciler) waitForChange(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, reason, message string,
) (ctrl.Result, error) {
	return ctrl.Result{}, r.ready(ctx, m, metav1.ConditionFalse, reason, message)
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
	m.Status.ObservedReconcileRequest = m.Annotations[ichorv1alpha1.EtcdRestoreReconcileAnnotation]

	return r.Status().Update(ctx, m)
}

// setWarnings records dry-run's warnings as the ConfigWarnings condition,
// mutating m in place; the caller persists it together with the Ready
// condition in the same status write. It is only called once a dry run has
// actually happened this reconcile, so a reconcile that fails before that
// point leaves a previous ConfigWarnings condition alone rather than
// erasing it.
//
// warnings is free-form text from Talos (machined's ValidateAtRuntime),
// never validated as safe to surface as-is -- the scrubber strips
// anything matching bundle's own secret-shaped scalars before it reaches
// this Condition, which internal/uiapi and anyone with talosmachines:get
// can read, a far wider readership than Secret RBAC allows.
func (r *TalosMachineReconciler) setWarnings(m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle, warnings []string) {
	status := metav1.ConditionFalse
	message := "dry run reported no warnings"

	if len(warnings) > 0 {
		status = metav1.ConditionTrue
		var scrub scrubber

		scrub.add(bundleSecretMaterial(bundle)...)
		message = scrub.clean(strings.Join(warnings, "; "))
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigWarnings, Status: status, Reason: reasonDryRunWarnings, Message: message,
	})
}

// renderMachine resolves cluster's system-layer inputs and renders m, returning the inputs so the
// caller fingerprints the same TalosStorages it rendered. Registry Secrets are read in
// ControllerNamespace, the same fixed namespace as loadBundle and never the TalosCluster's own; resolution errors
// name the Secret, never its data.
func (r *TalosMachineReconciler) renderMachine(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle, patches []ichorv1alpha1.TalosConfigPatch,
) ([]byte, string, *render.SystemInputs, error) {
	sys, err := render.ResolveSystemInputs(ctx, r.Client, r.ControllerNamespace, cluster)
	if err != nil {
		return nil, "", nil, err
	}

	rendered, hash, err := render.Render(cluster, m, bundle, patches, sys)

	return rendered, hash, sys, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TalosStorage, TalosConfigPatch and TalosCluster are render inputs: creating,
	// editing or deleting one re-reconciles the machines of its cluster, which is how a change
	// reaches an already converged node. Only a spec change (generation) counts for
	// an update, so the status writes these controllers make do not wake every machine.
	// A Secret that a TalosCluster names in registries[].authSecretRef is one too, in the
	// controller's namespace only.
	renderInput := builder.WithPredicates(predicate.GenerationChangedPredicate{})
	enqueue := handler.EnqueueRequestsFromMapFunc(r.machinesOfCluster)

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ichorv1alpha1.TalosCluster{},
		registryAuthSecretIndex, registryAuthSecretNames); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ichorv1alpha1.TalosCluster{},
		bundleSecretIndex, bundleSecretNames); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosMachine{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.machinesOfAuthSecret)).
		Watches(&ichorv1alpha1.TalosMachine{}, handler.EnqueueRequestsFromMapFunc(r.controlPlaneSiblings), renderInput).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.machinesOfOIDCTrust)).
		Watches(&ichorv1alpha1.TalosStorage{}, enqueue, renderInput).
		Watches(&ichorv1alpha1.TalosConfigPatch{}, enqueue, renderInput).
		Watches(&ichorv1alpha1.TalosCluster{}, enqueue, builder.WithPredicates(
			predicate.Or(predicate.GenerationChangedPredicate{}, clusterStatusRenderInputChanged()))).
		Named("talosmachine").
		Complete(r)
}

// clusterStatusRenderInputChanged fires on an update only when one of the two TalosCluster.status
// values desiredInputsHash reads moves: the CNIHandover condition flipping and the kube-proxy mode
// from status.cni.kubeProxy. Other status writes, which are frequent, do not match.
func clusterStatusRenderInputChanged() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldC, ok1 := e.ObjectOld.(*ichorv1alpha1.TalosCluster)
			newC, ok2 := e.ObjectNew.(*ichorv1alpha1.TalosCluster)
			if !ok1 || !ok2 {
				return false
			}

			oldMode, oldFrom := render.KubeProxyMode(oldC)
			newMode, newFrom := render.KubeProxyMode(newC)

			return apimeta.IsStatusConditionTrue(oldC.Status.Conditions, render.ConditionCNIHandover) !=
				apimeta.IsStatusConditionTrue(newC.Status.Conditions, render.ConditionCNIHandover) ||
				oldMode != newMode || oldFrom != newFrom ||
				render.KubeletServingVerified(oldC) != render.KubeletServingVerified(newC)
		},
	}
}

// machinesOfCluster maps a render input (TalosStorage, TalosConfigPatch or TalosCluster) to every
// TalosMachine of its cluster, to re-reconcile them. It selects machines to enqueue; it does not
// count control planes or gate anything, so a cached read is fine (countplanes_guard_test.go).
func (r *TalosMachineReconciler) machinesOfCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	var clusterName string

	switch o := obj.(type) {
	case *ichorv1alpha1.TalosCluster:
		clusterName = o.Name
	case *ichorv1alpha1.TalosConfigPatch:
		clusterName = o.Spec.ClusterRef.Name
	case *ichorv1alpha1.TalosStorage:
		clusterName = o.Spec.ClusterRef.Name
	default:
		return nil
	}

	var list ichorv1alpha1.TalosMachineList
	if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	var reqs []reconcile.Request

	for i := range list.Items {
		if list.Items[i].Spec.ClusterRef.Name == clusterName {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}

	return reqs
}

// registryAuthSecretIndex indexes a TalosCluster by the names of the Secrets its registries
// reference, so a Secret event finds the clusters that use it without scanning them.
const registryAuthSecretIndex = "spec.registries.authSecretRef.name"

// registryAuthSecretNames is the index function behind registryAuthSecretIndex.
func registryAuthSecretNames(o client.Object) []string {
	cluster, ok := o.(*ichorv1alpha1.TalosCluster)
	if !ok {
		return nil
	}

	var names []string

	for _, reg := range cluster.Spec.Registries {
		if reg.AuthSecretRef != nil {
			names = append(names, reg.AuthSecretRef.Name)
		}
	}

	return names
}

// machinesOfOIDCTrust maps the ConfigMap ichor-oidc-trust (a render input) to the
// machines of every TalosCluster that enables Ichor's issuer, so a changed CA or ClusterIP reaches
// converged nodes like any other desired-state change. Only the controller's namespace counts.
func (r *TalosMachineReconciler) machinesOfOIDCTrust(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.ControllerNamespace == "" || obj.GetNamespace() != r.ControllerNamespace || obj.GetName() != render.OIDCTrustConfigMap {
		return nil
	}

	var clusters ichorv1alpha1.TalosClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return nil
	}

	var reqs []reconcile.Request

	for i := range clusters.Items {
		if a := clusters.Items[i].Spec.Authentication; a != nil && a.Ichor != nil && a.Ichor.Enabled {
			reqs = append(reqs, r.machinesOfCluster(ctx, &clusters.Items[i])...)
		}
	}

	return reqs
}

// bundleSecretIndex indexes a TalosCluster by the name of its secrets bundle Secret.
const bundleSecretIndex = "spec.secretsRef.name"

// bundleSecretNames is the index function behind bundleSecretIndex.
func bundleSecretNames(o client.Object) []string {
	cluster, ok := o.(*ichorv1alpha1.TalosCluster)
	if !ok || cluster.Spec.SecretsRef.Name == "" {
		return nil
	}

	return []string{cluster.Spec.SecretsRef.Name}
}

// controlPlaneSiblings maps a control plane machine to its cluster's other machines, so one
// held for ControlPlaneOverCap is re-evaluated when a sibling is created or deleted.
func (r *TalosMachineReconciler) controlPlaneSiblings(ctx context.Context, obj client.Object) []reconcile.Request {
	m, ok := obj.(*ichorv1alpha1.TalosMachine)
	if !ok || m.Spec.Role != roleControlPlane {
		return nil
	}

	// machinesOfCluster keys on the cluster name and namespace; a patch stands in for them.
	var key ichorv1alpha1.TalosConfigPatch

	key.Namespace, key.Spec.ClusterRef = m.Namespace, m.Spec.ClusterRef

	return r.machinesOfCluster(ctx, &key)
}

// machinesOfAuthSecret maps a Secret to the machines of every TalosCluster whose registries
// reference it or whose secretsRef names it. Only Secrets in the controller's own namespace count: that is the one place
// ResolveRegistryAuth reads, and the cache holds no other (cache.go).
func (r *TalosMachineReconciler) machinesOfAuthSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.ControllerNamespace == "" || obj.GetNamespace() != r.ControllerNamespace {
		return nil
	}

	var reqs []reconcile.Request

	for _, index := range []string{registryAuthSecretIndex, bundleSecretIndex} {
		var clusters ichorv1alpha1.TalosClusterList
		if err := r.List(ctx, &clusters, client.MatchingFields{index: obj.GetName()}); err != nil {
			continue
		}

		for i := range clusters.Items {
			reqs = append(reqs, r.machinesOfCluster(ctx, &clusters.Items[i])...)
		}
	}

	return reqs
}

// reportHold keeps the AdoptionHeld condition in step with the hold-apply annotation.
// While it is set and the node differs from the render, stop=true: the drift is reported on Ready
// and nothing is applied.
func (r *TalosMachineReconciler) reportHold(
	ctx context.Context, m *ichorv1alpha1.TalosMachine, current, rendered []byte, hash string,
) (ctrl.Result, bool, error) {
	if m.Annotations[ichorv1alpha1.TalosMachineHoldApplyAnnotation] != strconv.FormatBool(true) {
		apimeta.RemoveStatusCondition(&m.Status.Conditions, conditionAdoptionHeld)

		return ctrl.Result{}, false, nil
	}

	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionAdoptionHeld, Status: metav1.ConditionTrue, Reason: reasonAdoptionHeld,
		Message: "annotation " + ichorv1alpha1.TalosMachineHoldApplyAnnotation + " is set; drift is reported and nothing is applied",
	})

	paths, err := changedPaths(current, rendered, hash)
	if err != nil {
		res, err := r.retryLater(ctx, m, reasonConfigDiffFailed, "comparing current and rendered configuration failed")

		return res, true, err
	}

	// No path: the node already has the render, in the same bytes or in another form. Not held
	// back; the caller's convergence check reports it Converged.
	if len(paths) == 0 {
		return ctrl.Result{}, false, nil
	}

	return ctrl.Result{RequeueAfter: reachabilityRecheck}, true, r.ready(ctx, m, metav1.ConditionFalse, reasonAdoptionHeld,
		fmt.Sprintf("held by %s, nothing was applied (drifted paths: %s)",
			ichorv1alpha1.TalosMachineHoldApplyAnnotation, strings.Join(paths, ", ")))
}
