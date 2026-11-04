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

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/secureboot"
)

// BootSecurityReconciler sets the cluster-level SecureBoot and KernelLockdown conditions from the
// approved machines' own. It is its own controller, not part of TalosClusterReconciler:
// that one runs discovery synchronously, and a machine's status change must not start a sweep.
type BootSecurityReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosmachines,verbs=get;list;watch

// Reconcile summarises the machines' conditions of each option the cluster sets (or any machine
// reports), and removes the cluster condition of an option no one uses any more.
func (r *BootSecurityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster ichorv1alpha1.TalosCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	approved, err := deleteguard.ApprovedMachines(ctx, r.Client, cluster.Namespace, cluster.Name, deleteguard.MachineFilter{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing machines: %w", err)
	}

	machines := map[string][]metav1.Condition{}

	for i := range approved {
		machines[approved[i].Name] = approved[i].Status.Conditions
	}

	base := cluster.DeepCopy()
	sec := cluster.Spec.Security

	for typ, optionSet := range map[string]bool{
		ichorv1alpha1.ConditionSecureBoot:     secureboot.Mode(sec) != ichorv1alpha1.SecureBootOff,
		ichorv1alpha1.ConditionKernelLockdown: sec != nil && sec.KernelLockdown != "",
	} {
		if !optionSet && !anyHas(machines, typ) {
			apimeta.RemoveStatusCondition(&cluster.Status.Conditions, typ)

			continue
		}

		c := secureboot.ClusterCondition(typ, machines)
		c.ObservedGeneration = cluster.Generation
		apimeta.SetStatusCondition(&cluster.Status.Conditions, c)
	}

	if apiequality.Semantic.DeepEqual(base.Status.Conditions, cluster.Status.Conditions) {
		return ctrl.Result{}, nil
	}

	if err := r.Status().Patch(ctx, &cluster, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, fmt.Errorf("writing the boot security conditions: %w", err)
	}

	return ctrl.Result{}, nil
}

// anyHas reports whether any machine carries a condition of type typ.
func anyHas(machines map[string][]metav1.Condition, typ string) bool {
	for _, conds := range machines {
		if apimeta.FindStatusCondition(conds, typ) != nil {
			return true
		}
	}

	return false
}

// machineCluster maps a TalosMachine to its cluster.
func machineCluster(_ context.Context, obj client.Object) []reconcile.Request {
	m, ok := obj.(*ichorv1alpha1.TalosMachine)
	if !ok || m.Spec.ClusterRef.Name == "" {
		return nil
	}

	return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}}}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootSecurityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ichorv1alpha1.TalosCluster{}).
		Watches(&ichorv1alpha1.TalosMachine{}, handler.EnqueueRequestsFromMapFunc(machineCluster)).
		Named("bootsecurity").
		Complete(r)
}
