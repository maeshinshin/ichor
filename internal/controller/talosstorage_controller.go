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
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// TalosStorage status condition types and reasons.
const (
	// conditionStorageReady is the condition Argo CD's health check reads: True once every
	// approved machine this TalosStorage governs has converged on a render that includes it.
	conditionStorageReady = "Ready"

	// conditionStorageSystem is informational and never gates Ready: whether the storage
	// system (layer 3) is present and configured to honour the registration layer 1 renders.
	conditionStorageSystem = "StorageSystemConsistent"

	// conditionStorageExtensions is informational and never gates Ready: whether every node this
	// TalosStorage governs runs the Talos system extensions its preset needs.
	conditionStorageExtensions = "ExtensionsPresent"

	// talosExtensionLabelPrefix is the Node label prefix Talos sets for each installed system
	// extension (extensions.talos.dev/<name>=<version>); the preset names them siderolabs/<name>.
	talosExtensionLabelPrefix = "extensions.talos.dev/"

	reasonStorageExtensionsPresent = "AllPresent"
	reasonStorageMissingExtensions = "MissingExtensions"
	reasonStorageExtensionsUnknown = "NodesNotObserved"

	reasonStoragePrepared         = "Prepared"
	reasonStorageNodesNotReady    = "NodesNotConverged"
	reasonStorageNoNodes          = "NoNodesSelected"
	reasonStorageClusterNotFound  = "ClusterNotFound"
	reasonStorageSystemConsistent = "Consistent"
	reasonStorageSystemMismatch   = "SettingDisabled"
	reasonStorageSystemAbsent     = "NotInstalled"

	// storageRequeueInterval re-reads the storage system's setting, which cannot be watched
	// (its CRDs may not exist at all). Machine and TalosStorage changes are watched instead.
	storageRequeueInterval = 2 * time.Minute

	// longhornCreateDefaultDiskLabeledNodes is the name of Longhorn's setting that makes it
	// honour the node label and annotation the longhorn-v1 preset renders. In
	// settings.longhorn.io/v1beta2 the value is the top-level string field "value" (longhorn-manager
	// v1.9.1, k8s/pkg/apis/longhorn/v1beta2/setting.go and types/setting.go), not under spec.
	longhornCreateDefaultDiskLabeledNodes = "create-default-disk-labeled-nodes"
)

var longhornSettingGVK = longhornNodeGVK.GroupVersion().WithKind("Setting")

// TalosStorageReconciler reports whether the Talos side of a TalosStorage (layer 1) is ready.
// It writes status only; the preparation itself reaches nodes through TalosMachineReconciler,
// which renders it as the system layer (pkg/render). It never writes a Longhorn object.
type TalosStorageReconciler struct {
	client.Client

	// APIReader reads the storage system's own setting uncached: the manager's cache would
	// start an informer on a Longhorn kind whose CRD may not exist.
	APIReader client.Reader

	// LonghornNamespace is where the storage system's settings live; defaults to
	// defaultLonghornNamespace.
	LonghornNamespace string

	// ControllerNamespace is where registry auth Secrets are read.
	ControllerNamespace string
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=longhorn.io,resources=settings,resourceNames=create-default-disk-labeled-nodes,verbs=get

// Reconcile computes Ready and StorageSystemConsistent for one TalosStorage.
func (r *TalosStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ts ichorv1alpha1.TalosStorage
	if err := r.Get(ctx, req.NamespacedName, &ts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ready, governed, err := r.readyCondition(ctx, &ts)
	if err != nil {
		return ctrl.Result{}, err
	}

	system, err := r.systemCondition(ctx, &ts)
	if err != nil {
		return ctrl.Result{}, err
	}

	extensions, err := r.extensionsCondition(ctx, &ts, governed)
	if err != nil {
		return ctrl.Result{}, err
	}

	apimeta.SetStatusCondition(&ts.Status.Conditions, ready)
	apimeta.SetStatusCondition(&ts.Status.Conditions, extensions)
	apimeta.SetStatusCondition(&ts.Status.Conditions, system)
	ts.Status.ObservedGeneration = ts.Generation

	if err := r.Status().Update(ctx, &ts); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: storageRequeueInterval}, nil
}

// readyCondition is True when at least one approved machine is governed by ts and every such
// machine is converged. "Governed" is render.StorageFor -- the same call Render makes, over the
// same TalosStorages -- and "converged" is the machine's Ready condition plus an
// observedInputsHash equal to the one TalosMachineReconciler would compute now (so a machine
// that converged before this TalosStorage existed or changed does not count). It lists machines
// to report on, not to count control planes or gate a disruptive action, so a cached read is
// fine (countplanes_guard_test.go).
func (r *TalosStorageReconciler) readyCondition(
	ctx context.Context, ts *ichorv1alpha1.TalosStorage,
) (metav1.Condition, []*ichorv1alpha1.TalosMachine, error) {
	cond := metav1.Condition{Type: conditionStorageReady}

	var cluster ichorv1alpha1.TalosCluster

	key := types.NamespacedName{Namespace: ts.Namespace, Name: ts.Spec.ClusterRef.Name}
	if err := r.Get(ctx, key, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			cond.Status, cond.Reason = metav1.ConditionFalse, reasonStorageClusterNotFound
			cond.Message = fmt.Sprintf("TalosCluster %q not found", key.Name)

			return cond, nil, nil
		}

		return cond, nil, err
	}

	var (
		storages ichorv1alpha1.TalosStorageList
		patches  ichorv1alpha1.TalosConfigPatchList
	)

	for _, l := range []client.ObjectList{&storages, &patches} {
		if err := r.List(ctx, l, client.InNamespace(ts.Namespace)); err != nil {
			return cond, nil, err
		}
	}

	// The same credentials the machine controller fingerprints. Unresolvable ones
	// leave the fingerprint empty, which cannot equal a converged machine's, so they read as pending.
	auth, _ := render.ResolveRegistryAuth(ctx, r.Client, r.ControllerNamespace, &cluster)
	authHash := registryAuthFingerprint(auth)

	machines, err := deleteguard.ApprovedMachines(ctx, r.Client, ts.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return cond, nil, err
	}

	var (
		governed []*ichorv1alpha1.TalosMachine
		pending  []string
	)

	for i := range machines {
		m := &machines[i]

		chosen, err := render.StorageFor(&cluster, m, storages.Items)
		if err != nil {
			return cond, nil, err
		}

		if chosen == nil || chosen.Name != ts.Name {
			continue
		}

		governed = append(governed, m)

		want, err := desiredInputsHash(&cluster, m, patches.Items, storages.Items, authHash)
		if err != nil {
			return cond, nil, err
		}

		if !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionReady) || m.Status.ObservedInputsHash != want {
			pending = append(pending, m.Name)
		}
	}

	slices.Sort(pending)

	switch {
	case len(governed) == 0:
		cond.Status, cond.Reason = metav1.ConditionFalse, reasonStorageNoNodes
		cond.Message = "no approved node is governed by this TalosStorage " +
			"(spec.nodes matches each node's spec.nodeLabels; a node matched by several TalosStorages belongs to the first by name)"
	case len(pending) > 0:
		cond.Status, cond.Reason = metav1.ConditionFalse, reasonStorageNodesNotReady
		cond.Message = fmt.Sprintf("%d of %d nodes have not converged on a configuration that includes this TalosStorage: %s",
			len(pending), len(governed), strings.Join(pending, ", "))
	default:
		cond.Status, cond.Reason = metav1.ConditionTrue, reasonStoragePrepared
		cond.Message = fmt.Sprintf("%d nodes carry the %s preparation", len(governed), ts.Spec.DiskPreset)
	}

	return cond, governed, nil
}

// extensionsCondition checks, for every governed machine, that the Talos system extensions the
// preset needs are installed. The source is the Kubernetes Node's extensions.talos.dev/<name>
// labels, which Talos itself sets from the running image: they need no Talos API client or
// credentials here and reflect what is actually booted, not what a schematic ID says. A machine
// whose Node cannot be found yet is skipped; if no Node could be observed at all the condition is
// Unknown. It never gates Ready and never blocks the rest of the reconcile.
func (r *TalosStorageReconciler) extensionsCondition(
	ctx context.Context, ts *ichorv1alpha1.TalosStorage, governed []*ichorv1alpha1.TalosMachine,
) (metav1.Condition, error) {
	cond := metav1.Condition{Type: conditionStorageExtensions}

	preset, err := storage.NewPreset(ts.Spec.DiskPreset)
	if err != nil {
		return cond, err
	}

	var problems []string

	observed := 0

	for _, m := range governed {
		name, err := resolveNodeName(ctx, r.Client, nil, m)
		if err != nil {
			continue
		}

		var node corev1.Node
		if err := r.Get(ctx, types.NamespacedName{Name: name}, &node); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}

			return cond, err
		}

		observed++

		var installed []string

		for k := range node.Labels {
			if ext, ok := strings.CutPrefix(k, talosExtensionLabelPrefix); ok {
				installed = append(installed, "siderolabs/"+ext)
			}
		}

		if preset.CheckExtensions(installed) == nil {
			continue
		}

		var missing []string

		for _, want := range preset.RequiredExtensions() {
			if !slices.Contains(installed, want) {
				missing = append(missing, want)
			}
		}

		problems = append(problems, fmt.Sprintf("%s lacks %s", name, strings.Join(missing, ", ")))
	}

	slices.Sort(problems)

	switch {
	case len(problems) > 0:
		cond.Status, cond.Reason = metav1.ConditionFalse, reasonStorageMissingExtensions
		cond.Message = fmt.Sprintf("%s: %s. Add them to spec.schematic and run ichor upgrade talos",
			ts.Spec.DiskPreset, strings.Join(problems, "; "))
	case observed == 0:
		cond.Status, cond.Reason = metav1.ConditionUnknown, reasonStorageExtensionsUnknown
		cond.Message = "no Kubernetes Node of a governed node was found, so its extensions are not known yet"
	default:
		cond.Status, cond.Reason = metav1.ConditionTrue, reasonStorageExtensionsPresent
		cond.Message = fmt.Sprintf("%d nodes carry the system extensions %s needs", observed, ts.Spec.DiskPreset)
	}

	return cond, nil
}

// systemCondition reads the storage system's own setting through the preset's consistency check.
// An absent storage system is Unknown/NotInstalled, not an error: installing it is Argo CD's job
// (layer 2), and this condition does not gate Ready.
func (r *TalosStorageReconciler) systemCondition(
	ctx context.Context, ts *ichorv1alpha1.TalosStorage,
) (metav1.Condition, error) {
	cond := metav1.Condition{Type: conditionStorageSystem}

	preset, err := storage.NewPreset(ts.Spec.DiskPreset)
	if err != nil {
		return cond, err
	}

	ns := r.LonghornNamespace
	if ns == "" {
		ns = defaultLonghornNamespace
	}

	var setting unstructured.Unstructured
	setting.SetGroupVersionKind(longhornSettingGVK)

	err = r.APIReader.Get(ctx, types.NamespacedName{Namespace: ns, Name: longhornCreateDefaultDiskLabeledNodes}, &setting)

	switch {
	case apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err):
		cond.Status, cond.Reason = metav1.ConditionUnknown, reasonStorageSystemAbsent
		cond.Message = fmt.Sprintf("the storage system is not installed (no %s setting in namespace %q)",
			longhornCreateDefaultDiskLabeledNodes, ns)

		return cond, nil
	case err != nil:
		return cond, fmt.Errorf("reading the storage system's %s setting: %w", longhornCreateDefaultDiskLabeledNodes, err)
	}

	value, _, _ := unstructured.NestedString(setting.Object, "value")

	if err := preset.CheckConsistency(storage.LiveConfig{CreateDefaultDiskOnLabeledNodes: value == "true"}); err != nil {
		cond.Status, cond.Reason, cond.Message = metav1.ConditionFalse, reasonStorageSystemMismatch, err.Error()

		return cond, nil
	}

	cond.Status, cond.Reason = metav1.ConditionTrue, reasonStorageSystemConsistent
	cond.Message = "the storage system honours the node label and annotation this preset renders"

	return cond, nil
}

// SetupWithManager sets up the controller with the Manager. Own-status writes do not retrigger
// it (generation filter); TalosMachine changes -- a machine converging is what flips Ready --
// map to every TalosStorage in the machine's namespace and cluster.
func (r *TalosStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosStorage{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&ichorv1alpha1.TalosMachine{}, handler.EnqueueRequestsFromMapFunc(r.storagesForMachine)).
		Named("talosstorage").
		Complete(r)
}

func (r *TalosStorageReconciler) storagesForMachine(ctx context.Context, obj client.Object) []reconcile.Request {
	m, ok := obj.(*ichorv1alpha1.TalosMachine)
	if !ok {
		return nil
	}

	var list ichorv1alpha1.TalosStorageList
	if err := r.List(ctx, &list, client.InNamespace(m.Namespace)); err != nil {
		return nil
	}

	var reqs []reconcile.Request

	for _, s := range list.Items {
		if s.Spec.ClusterRef.Name == m.Spec.ClusterRef.Name {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
		}
	}

	return reqs
}
