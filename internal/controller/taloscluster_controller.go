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
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// discoveryRequeueInterval is how often a TalosCluster with a non-empty
// spec.discovery gets re-probed. DiscoverAll's own worst case, at the CRD's
// caps, is about 68s (1088 candidates / 32 workers * discoveryDialTimeout);
// five minutes keeps successive runs from overlapping with room to spare,
// and matches clusterlock.DefaultDuration as a familiar order of magnitude
// for "how often is reasonable" elsewhere in this codebase.
const discoveryRequeueInterval = 5 * time.Minute

// TalosClusterReconciler drives discovery for one TalosCluster
// (docs/design/architecture.md "Nodes"; ADR 0011): it holds no cluster-level
// status of its own -- every outcome Discover/DiscoverAll produce (a
// pending TalosMachine, a WeakIdentity/IdentityCollision Event) already
// lands on the right object -- and exists only to decide *when*
// DiscoverAll runs, the question ADR 0011 left open.
//
// DiscoverAll runs synchronously inside Reconcile and can take up to its
// own worst case (see discoveryRequeueInterval) before returning.
// controller-runtime's default MaxConcurrentReconciles is 1 per controller,
// so a slow discovery pass blocks every other TalosCluster this controller
// would otherwise reconcile for that long. Nothing latency-sensitive
// belongs in this reconciler for that reason -- phase 4's hand-over is
// TalosCluster-level work and would be an easy, wrong place to add it.
type TalosClusterReconciler struct {
	client.Client

	// Discovery runs the actual probe (EnumerateCandidates + Discover per
	// candidate); this reconciler only decides when to call it.
	Discovery *DiscoveryReconciler
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch

// Reconcile calls Discovery.DiscoverAll for cluster's declared range and
// reschedules itself at discoveryRequeueInterval -- but only while
// spec.discovery is non-empty. A cluster with no discovery declared is
// never requeued: ADR 0011's "disabled by default, never scans on its own"
// guarantee holds here too, not just inside DiscoverAll itself.
func (r *TalosClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := adviseCARotation(ctx, r.Client, &cluster); err != nil {
		return ctrl.Result{}, err
	}

	if cluster.Spec.Discovery == nil {
		return ctrl.Result{}, nil
	}

	if _, err := r.Discovery.DiscoverAll(ctx, &cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: discoveryRequeueInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TalosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Watches(&ichorv1alpha1.TalosOperation{}, handler.EnqueueRequestsFromMapFunc(caRotationOperationClusters)).
		Watches(&ichorv1alpha1.MachineReclaim{}, handler.EnqueueRequestsFromMapFunc(reclaimDoneClusters)).
		Named("taloscluster").
		Complete(r)
}
