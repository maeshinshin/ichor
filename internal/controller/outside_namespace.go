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

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// +kubebuilder:rbac:groups=ichor.maesh.dev,resources=talosconfigpatches/status,verbs=get;update;patch

// outsideNamespaceKinds is every namespaced kind whose status carries conditions. TalosCredentialRequest has none
// (it is answered once and short-lived) and IchorInstallation is cluster-scoped.
func outsideNamespaceKinds() map[string]func() client.Object {
	return map[string]func() client.Object{
		"talosclusters":      func() client.Object { return &ichorv1alpha1.TalosCluster{} },
		"talosmachines":      func() client.Object { return &ichorv1alpha1.TalosMachine{} },
		"talosconfigpatches": func() client.Object { return &ichorv1alpha1.TalosConfigPatch{} },
		"talosoperations":    func() client.Object { return &ichorv1alpha1.TalosOperation{} },
		"talosetcdbackups":   func() client.Object { return &ichorv1alpha1.TalosEtcdBackup{} },
		"talosstorages":      func() client.Object { return &ichorv1alpha1.TalosStorage{} },
		"machinereclaims":    func() client.Object { return &ichorv1alpha1.MachineReclaim{} },
		"ichorusers":         func() client.Object { return &ichorv1alpha1.IchorUser{} },
	}
}

// statusConditions is the condition list of an Ichor object's status.
func statusConditions(o client.Object) *[]metav1.Condition {
	switch o := o.(type) {
	case *ichorv1alpha1.TalosCluster:
		return &o.Status.Conditions
	case *ichorv1alpha1.TalosMachine:
		return &o.Status.Conditions
	case *ichorv1alpha1.TalosConfigPatch:
		return &o.Status.Conditions
	case *ichorv1alpha1.TalosOperation:
		return &o.Status.Conditions
	case *ichorv1alpha1.TalosEtcdBackup:
		return &o.Status.Conditions
	case *ichorv1alpha1.TalosStorage:
		return &o.Status.Conditions
	case *ichorv1alpha1.MachineReclaim:
		return &o.Status.Conditions
	case *ichorv1alpha1.IchorUser:
		return &o.Status.Conditions
	}

	return nil
}

// OutsideNamespaceReconciler gives an object outside the controller's namespace the informational condition
// OutsideControllerNamespace (ADR 0175). One instance serves one kind; the object is never moved, deleted or blocked,
// and the condition does not touch Ready.
type OutsideNamespaceReconciler struct {
	client.Client
	// Namespace is the controller's namespace.
	Namespace string
	// New returns an empty object of the kind served.
	New func() client.Object
}

// Reconcile sets the condition on an object outside Namespace and clears it on one inside.
func (r *OutsideNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := r.New()
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	conds := statusConditions(obj)
	if conds == nil || !obj.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	base, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return ctrl.Result{}, nil
	}

	if obj.GetNamespace() == r.Namespace {
		apimeta.RemoveStatusCondition(conds, ichorv1alpha1.ConditionOutsideControllerNamespace)
	} else {
		apimeta.SetStatusCondition(conds, metav1.Condition{
			Type: ichorv1alpha1.ConditionOutsideControllerNamespace, Status: metav1.ConditionTrue,
			Reason:             "OutsideControllerNamespace",
			ObservedGeneration: obj.GetGeneration(),
			Message: fmt.Sprintf("this object is in namespace %q and keeps reconciling there; new Ichor objects go to %q, "+
				"the namespace of Ichor's controller", obj.GetNamespace(), r.Namespace),
		})
	}

	if apiequality.Semantic.DeepEqual(statusConditions(base), conds) {
		return ctrl.Result{}, nil
	}

	// Optimistic lock: the conditions are an array, which a merge patch replaces whole from this possibly stale copy. A
	// conflict requeues and the next pass reads the object again.
	if err := r.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}

		return ctrl.Result{}, fmt.Errorf("writing the %s condition: %w", ichorv1alpha1.ConditionOutsideControllerNamespace, err)
	}

	return ctrl.Result{}, nil
}

// SetupOutsideNamespaceControllers registers one OutsideNamespaceReconciler per kind.
func SetupOutsideNamespaceControllers(mgr ctrl.Manager, namespace string) error {
	if namespace == "" { // no POD_NAMESPACE (a local run): every object would read as outside
		return nil
	}

	for name, newObj := range outsideNamespaceKinds() {
		r := &OutsideNamespaceReconciler{Client: mgr.GetClient(), Namespace: namespace, New: newObj}

		if err := ctrl.NewControllerManagedBy(mgr).For(newObj()).Named("outsidenamespace-" + name).Complete(quietExpected(r, "outsidenamespace-"+name)); err != nil {
			return fmt.Errorf("setting up the %s outside-namespace controller: %w", name, err)
		}
	}

	return nil
}
