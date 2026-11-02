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
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/endpointguard"
)

// nolint:unused
// log is for logging in this package.
var talosmachinelog = logf.Log.WithName("talosmachine-resource")

// SetupTalosMachineWebhookWithManager registers the webhook for TalosMachine in the manager.
func SetupTalosMachineWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &ichorv1alpha1.TalosMachine{}).
		WithValidator(&TalosMachineCustomValidator{APIReader: mgr.GetAPIReader(), BreakGlass: breakGlassCheckFor(mgr.GetClient()), Can: rightCheckFor(mgr.GetClient())}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-talosmachine,mutating=false,failurePolicy=fail,timeoutSeconds=5,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines,verbs=create;update;delete,versions=v1alpha1,name=vtalosmachine-v1alpha1.kb.io,admissionReviewVersions=v1

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
	// second, concurrent deletion look safe when it is not (ADR 0010's
	// appendix). Every count goes through pkg/deleteguard's Count*
	// functions, the one place machines are listed for counting. It also
	// reads the machine's TalosCluster (count, schedulable).
	APIReader client.Reader
	// BreakGlass lets a user holding the break-glass verb on the machine override the
	// last-worker guard (ADR 0065, amended). nil means no override.
	BreakGlass BreakGlassCheck
	// Can asks whether the requesting user holds a verb (ADR 0108): approve on
	// talosmachines/approval, and update on talosmachines/endpoint for an address
	// the cluster does not declare. nil means nobody does.
	Can RightCheck
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateCreate(ctx context.Context, obj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon creation", "name", obj.GetName())

	if err := refuseSystemNamespace("TalosMachine", obj.Namespace); err != nil {
		return nil, err
	}

	// A machine created already approved is an approval too.
	if err := v.checkEndpoint(ctx, nil, obj); err != nil {
		return nil, err
	}

	if err := v.checkApprover(ctx, nil, obj); err != nil {
		return nil, err
	}

	return nil, v.checkApprovalCap(ctx, nil, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type TalosMachine.
func (v *TalosMachineCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) (admission.Warnings, error) {
	talosmachinelog.Info("Validation for TalosMachine upon update", "name", newObj.GetName())

	if err := v.checkEndpoint(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	if err := v.checkApprover(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	if err := v.checkApprovalCap(ctx, oldObj, newObj); err != nil {
		return nil, err
	}

	// An approved worker that stops being one (rejected, re-roled, moved to
	// another cluster) leaves its old cluster just like a delete does.
	stillWorker := isApprovedRole(newObj, roleWorker) && sameCluster(oldObj, newObj)
	if isApprovedRole(oldObj, roleWorker) && !stillWorker {
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

// can asks the requesting user's right; a missing checker or a failed check refuses.
func (v *TalosMachineCustomValidator) can(ctx context.Context, verb, sub string, m *ichorv1alpha1.TalosMachine) error {
	if v.Can == nil {
		return fmt.Errorf("you need the %q verb on talosmachines/%s", verb, sub)
	}

	ok, err := v.Can(ctx, verb, "talosmachines", sub, m.Name, m.Namespace)
	if err != nil {
		return fmt.Errorf("cannot check the %q right on talosmachines/%s: %w", verb, sub, err)
	}

	if !ok {
		return fmt.Errorf("you need the %q verb on talosmachines/%s", verb, sub)
	}

	return nil
}

// checkApprover requires approve on talosmachines/approval for the transition into approved (at
// creation too): the controller then pushes the cluster's rendered configuration, with its CA keys,
// to the machine, on the requester's behalf.
func (v *TalosMachineCustomValidator) checkApprover(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) error {
	if !newObj.Spec.IsApproved() || (oldObj != nil && oldObj.Spec.IsApproved()) {
		return nil
	}

	if err := v.can(ctx, ichorv1alpha1.MachineApproveVerb, ichorv1alpha1.MachineApprovalSubresource, newObj); err != nil {
		return fmt.Errorf("approving a machine hands it the cluster's configuration: %w", err)
	}

	return nil
}

// checkEndpoint judges spec.endpoint when a machine is created, changes endpoint, or changes
// cluster: never loopback, link-local, unspecified, multicast or inside the cluster's own pod or
// service ranges; and only an address the cluster's spec.discovery declares, unless the user holds
// update on talosmachines/endpoint. An update that leaves both alone is not judged, so a machine
// whose address was declared once stays editable.
func (v *TalosMachineCustomValidator) checkEndpoint(ctx context.Context, oldObj, newObj *ichorv1alpha1.TalosMachine) error {
	if oldObj != nil && oldObj.Spec.Endpoint == newObj.Spec.Endpoint && sameCluster(oldObj, newObj) {
		return nil
	}

	cluster, err := v.getCluster(ctx, newObj)
	if err != nil {
		return err
	}

	if cluster == nil {
		cluster = &ichorv1alpha1.TalosCluster{}
	}

	if err := endpointguard.Forbidden(newObj.Spec.Endpoint, cluster); err != nil {
		return fmt.Errorf("spec.endpoint %q is refused: %w", newObj.Spec.Endpoint, err)
	}

	if endpointguard.InDiscovery(newObj.Spec.Endpoint, cluster) {
		return nil
	}

	if err := v.can(ctx, ichorv1alpha1.MachineEndpointVerb, ichorv1alpha1.MachineEndpointSubresource, newObj); err != nil {
		return fmt.Errorf("spec.endpoint %q is not an address of the cluster's spec.discovery (addresses, cidrs): %w", newObj.Spec.Endpoint, err)
	}

	return nil
}

// checkApprovalCap enforces "no approving more control planes than
// configured" (ADR 0054): only on the transition into an approved
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
	if err != nil || cluster == nil || !isUnschedulable(cluster) {
		return err
	}

	workers, err := deleteguard.CountApprovedWorkers(ctx, v.APIReader, m.Namespace, m.Spec.ClusterRef.Name)
	if err != nil {
		return err
	}

	return v.BreakGlass.checkLastWorker(ctx, workers-1, "talosmachines", m.Name, m.Namespace)
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
	// This check stays anyway (ADR 0010's appendix: kept, not replaced):
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
