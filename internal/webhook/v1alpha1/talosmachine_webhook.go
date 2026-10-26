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

package v1alpha1

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
)

// nolint:unused
// log is for logging in this package.
var talosmachinelog = logf.Log.WithName("talosmachine-resource")

// SetupTalosMachineWebhookWithManager registers the webhook for TalosMachine in the manager.
func SetupTalosMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosMachine{}).
		WithValidator(&TalosMachineCustomValidator{APIReader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-talosmachine,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines,verbs=create;update;delete,versions=v1alpha1,name=vtalosmachine-v1alpha1.kb.io,admissionReviewVersions=v1

// TalosMachineCustomValidator struct is responsible for validating the TalosMachine resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type TalosMachineCustomValidator struct {
	// APIReader lists sibling TalosMachines to count live control planes
	// for the quorum check, bypassing the manager's cache: a cached read
	// can still report a control plane deleted moments ago, drifting the
	// count high -- the dangerous direction, since it is what lets a
	// second, concurrent deletion look safe when it is not. Every count goes through pkg/deleteguard's Count*
	// functions, the one place machines are listed for counting. It also
	// reads the machine's TalosCluster (count, schedulable).
	APIReader client.Reader
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon creation", "name", obj.GetName())

	// A machine created already approved (GitOps) is an approval too.
	return nil, v.checkApprovalCap(ctx, nil, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon update", "name", newObj.GetName())

	if err := v.checkApprovalCap(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	// An approved worker that stops being one (rejected, re-roled, moved to
	// another cluster) leaves its old cluster just like a delete does.
	if isApprovedRole(oldObj, roleWorker) && !(isApprovedRole(newObj, roleWorker) && sameCluster(oldObj, newObj)) {
		return nil, v.checkWorkerLeaves(ctx, oldObj)
	}

	return nil, nil
}

const (
	roleControlPlane = "controlplane"
	roleWorker       = "worker"
)

func isApprovedRole(m *ichorv1alpha1.TalosMachine, role string) bool {
	return m != nil && m.Spec.Role == role && m.Spec.IsApproved()
}

func sameCluster(a, b *ichorv1alpha1.TalosMachine) bool {
	return a.Spec.ClusterRef.Name == b.Spec.ClusterRef.Name
}

// getCluster reads the machine's TalosCluster directly. A missing or
// deleting cluster returns nil, nil: there is nothing to compare against, and
// a cluster teardown must be able to cascade into its machines.
func (v *TalosMachineCustomValidator) getCluster(ctx context.Context, m *ichorv1alpha1.TalosMachine) (*ichorv1alpha1.TalosCluster, error) {
	var c ichorv1alpha1.TalosCluster
	if err := v.APIReader.Get(ctx, client.ObjectKey{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}, &c); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if !c.DeletionTimestamp.IsZero() {
		return nil, nil
	}

	return &c, nil
}

// checkApprovalCap enforces "no approving more control planes than
// configured": only on the transition into an approved
// control plane of a cluster (oldObj nil on create).
func (v *TalosMachineCustomValidator) checkApprovalCap(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) error {
	if !isApprovedRole(newObj, roleControlPlane) {
		return nil
	}
	if isApprovedRole(oldObj, roleControlPlane) && sameCluster(oldObj, newObj) {
		return nil
	}

	cluster, err := v.getCluster(ctx, newObj)
	if err != nil || cluster == nil {
		return err
	}

	// newObj is not stored as an approved control plane yet, so the count
	// excludes it.
	current, err := deleteguard.CountApprovedControlPlanes(ctx, v.APIReader, newObj.Namespace, newObj.Spec.ClusterRef.Name)
	if err != nil {
		return err
	}

	return deleteguard.CheckControlPlaneApproval(current, int(cluster.Spec.ControlPlane.Count))
}

// checkWorkerLeaves refuses removing the last approved worker of a cluster
// whose control planes are unschedulable. m is stored approved, so the count
// includes it.
func (v *TalosMachineCustomValidator) checkWorkerLeaves(ctx context.Context, m *ichorv1alpha1.TalosMachine) error {
	cluster, err := v.getCluster(ctx, m)
	if err != nil || cluster == nil || cluster.Spec.ControlPlane.Schedulable != "false" {
		return err
	}

	workers, err := deleteguard.CountApprovedWorkers(ctx, v.APIReader, m.Namespace, m.Spec.ClusterRef.Name)
	if err != nil {
		return err
	}

	return deleteguard.CheckLastWorker(cluster.Spec.ControlPlane.Schedulable, workers-1)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateDelete(ctx context.Context, obj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon deletion", "name", obj.GetName())

	if err := deleteguard.CheckDelete(obj.GetAnnotations()); err != nil {
		return nil, err
	}

	if isApprovedRole(obj, roleWorker) {
		return nil, v.checkWorkerLeaves(ctx, obj)
	}

	// Only an approved control plane has ever joined etcd (design: approval is
	// what lets the controller render and apply config); any other machine
	// cannot affect quorum, so the annotation check above is the whole story.
	if !isApprovedRole(obj, roleControlPlane) {
		return nil, nil
	}

	// shortcut: two concurrent deletes of different control planes in the
	// same cluster can both List before either delete commits, so both see
	// the pre-deletion count and both get admitted, together breaking
	// quorum. This is not a lock problem: admission is never serialized
	// with the commit it's gatekeeping, so no lock around this List closes
	// it -- counting objects can't enforce a cross-request invariant by
	// itself.
	//
	// The real fix -- design's "Nodes" ordered removal sequence -- now
	// exists (internal/controller/removal.go): a finalizer intercepts the
	// actual deletion and recounts under the per-cluster lock, where a
	// concurrent sibling removal has to wait its turn rather than racing.
	// This check stays anyway (not replaced):
	// it is the only one of the two that can refuse synchronously, at
	// admission time, with a message `kubectl delete` shows immediately,
	// and it still correctly refuses the single-delete case on its own.
	// removal.go's check is the authoritative one for the case this one
	// cannot see -- concurrent deletes of different control planes -- and
	// is what actually decides whether the object ever stops existing.
	controlPlanes, err := deleteguard.CountApprovedControlPlanes(ctx, v.APIReader, obj.Namespace, obj.Spec.ClusterRef.Name)
	if err != nil {
		return nil, err
	}

	return nil, deleteguard.CheckControlPlaneQuorum(controlPlanes)
}
