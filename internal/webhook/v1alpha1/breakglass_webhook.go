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
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

// BreakGlassVerb is the RBAC verb that lets a user write a GitOps-managed CR
// directly, or remove an Ichor finalizer from a deleting one.
const BreakGlassVerb = "break-glass"

const (
	breakGlassWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-breakglass"
	ichorFinalizerPrefix  = "ichor.maesh.dev/"
	ichorGroup            = "ichor.maesh.dev"
)

var breakglasslog = logf.Log.WithName("breakglass-webhook")

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines;talosconfigpatches,verbs=update;delete,versions=v1alpha1,name=vbreakglass-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosoperations,verbs=create,versions=v1alpha1,name=vbreakglassop-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

// BreakGlassOptions configures the break-glass admission check.
type BreakGlassOptions struct {
	// ArgoCDNamespace is where argocd-cm lives, for gitopstracking.IsTracked.
	ArgoCDNamespace string
	// ExemptUsers are the exact usernames never checked: Ichor's own
	// controller and the GitOps engines (see DefaultExemptUsers).
	ExemptUsers []string
}

// DefaultExemptUsers names the identities whose writes the check must never
// stop: by exact username, never by group or
// namespace, because a prefix such as system:serviceaccounts:argocd would
// also exempt argocd-server, which writes on behalf of people.
func DefaultExemptUsers(controllerNamespace, controllerServiceAccount, argoCDNamespace string) []string {
	sa := func(ns, name string) string { return "system:serviceaccount:" + ns + ":" + name }

	users := []string{
		sa(argoCDNamespace, "argocd-application-controller"),
		sa("flux-system", "kustomize-controller"),
		sa("flux-system", "helm-controller"),
		// A namespace or owner deletion cascades into tracked CRs.
		sa("kube-system", "namespace-controller"),
		sa("kube-system", "generic-garbage-collector"),
	}
	if controllerNamespace != "" && controllerServiceAccount != "" {
		users = append(users, sa(controllerNamespace, controllerServiceAccount))
	}

	return users
}

// BreakGlassHandler rejects a direct write to a GitOps-tracked CR, and the
// removal of an ichor.maesh.dev finalizer from a deleting one, unless the
// requesting user holds the break-glass verb on that object.
type BreakGlassHandler struct {
	// Client is uncached: it reads argocd-cm and Flux Kustomizations (no
	// informer, no list/watch grant) and creates SubjectAccessReviews.
	Client          client.Client
	ArgoCDNamespace string
	ExemptUsers     []string
}

// SetupBreakGlassWebhookWithManager registers the check on the manager's webhook server.
func SetupBreakGlassWebhookWithManager(mgr ctrl.Manager, opts BreakGlassOptions) error {
	direct, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("creating the uncached client: %w", err)
	}

	h := &BreakGlassHandler{Client: direct, ArgoCDNamespace: opts.ArgoCDNamespace, ExemptUsers: opts.ExemptUsers}
	mgr.GetWebhookServer().Register(breakGlassWebhookPath, &admission.Webhook{Handler: h})

	return nil
}

// Handle implements admission.Handler.
func (h *BreakGlassHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	if req.Resource.Resource == "talosoperations" {
		return h.handleOperation(ctx, req)
	}

	if req.Operation != "UPDATE" && req.Operation != "DELETE" {
		return admission.Allowed("")
	}

	if slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("exempt: Ichor's controller or a GitOps engine")
	}

	var old unstructured.Unstructured
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored object: %w", err))
	}

	removing := false

	if req.Operation == "UPDATE" {
		var updated unstructured.Unstructured
		if err := json.Unmarshal(req.Object.Raw, &updated); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the new object: %w", err))
		}

		removing = removesIchorFinalizer(&old, &updated)
	}

	// Ask the user's right first: holders skip the tracking lookup, so a
	// break-glass write still works while Argo CD or Flux is unreadable.
	allowed, err := h.mayBreakGlass(ctx, req.UserInfo, req.Resource.Resource, req.Name, req.Namespace)
	if err != nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", BreakGlassVerb, err))
	}

	if allowed {
		breakglasslog.Info("write admitted under break-glass", "user", req.UserInfo.Username, "name", req.Name, "operation", req.Operation)

		return admission.Allowed("")
	}

	if removing {
		return admission.Denied(fmt.Sprintf("removing an %s* finalizer from a deleting %s is refused unless you hold the %q verb "+
			"(the controller is still working on it: this orphans a node or an etcd member)", ichorFinalizerPrefix, req.Resource.Resource, BreakGlassVerb))
	}

	tracked, by, err := gitopstracking.IsTracked(ctx, h.Client, h.ArgoCDNamespace, &old)
	if err != nil {
		// Cannot tell, do not write. Argo CD and Flux are exempt
		// above, so refusing here never blocks a sync.
		return admission.Errored(http.StatusServiceUnavailable,
			fmt.Errorf("cannot tell whether %s is managed by GitOps, so the direct write is refused: %w", req.Name, err))
	}

	if tracked {
		return admission.Denied(fmt.Sprintf("%s %q is managed by %s: change it in Git, or hold the %q verb to write it directly in an incident",
			req.Resource.Resource, req.Name, by, BreakGlassVerb))
	}

	return admission.Allowed("")
}

// handleOperation guards the one dangerous node action, reset, wherever the
// TalosOperation comes from: the CLI and UI check break-glass too, but a user
// with `create` on talosoperations can write the CR by hand. The
// typed-name confirmation is a CRD rule (confirm == machineRef.name), so it
// needs no copy here.
func (h *BreakGlassHandler) handleOperation(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != "CREATE" || slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
	}

	var op ichorv1alpha1.TalosOperation
	if err := json.Unmarshal(req.Object.Raw, &op); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the operation: %w", err))
	}

	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindNode || op.Spec.Node == nil || op.Spec.Node.Action != ichorv1alpha1.NodeActionReset {
		return admission.Allowed("")
	}

	machine := op.Spec.Node.MachineRef.Name

	allowed, err := h.mayBreakGlass(ctx, req.UserInfo, "talosmachines", machine, req.Namespace)
	if err != nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", BreakGlassVerb, err))
	}

	if !allowed {
		return admission.Denied(fmt.Sprintf("a reset wipes the node: it needs the %q verb on talosmachines/%s", BreakGlassVerb, machine))
	}

	breakglasslog.Info("reset admitted under break-glass", "user", req.UserInfo.Username, "machine", machine)

	return admission.Allowed("")
}

// removesIchorFinalizer reports whether updated drops an ichor.maesh.dev/*
// finalizer that old (already deleting) carries.
func removesIchorFinalizer(old, updated *unstructured.Unstructured) bool {
	if old.GetDeletionTimestamp() == nil {
		return false
	}

	for _, f := range old.GetFinalizers() {
		if strings.HasPrefix(f, ichorFinalizerPrefix) && !slices.Contains(updated.GetFinalizers(), f) {
			return true
		}
	}

	return false
}

// mayBreakGlass asks Kubernetes whether the requesting user holds the verb on
// the object, as the user: the webhook never impersonates.
func (h *BreakGlassHandler) mayBreakGlass(ctx context.Context, user authnv1.UserInfo, resource, name, namespace string) (bool, error) {
	sar := &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   user.Username,
			UID:    user.UID,
			Groups: user.Groups,
			Extra:  extraOf(user.Extra),
			ResourceAttributes: &authzv1.ResourceAttributes{
				Group:     ichorGroup,
				Resource:  resource,
				Verb:      BreakGlassVerb,
				Name:      name,
				Namespace: namespace,
			},
		},
	}

	if err := h.Client.Create(ctx, sar); err != nil {
		return false, err
	}

	return sar.Status.Allowed, nil
}

func extraOf(in map[string]authnv1.ExtraValue) map[string]authzv1.ExtraValue {
	if in == nil {
		return nil
	}

	out := make(map[string]authzv1.ExtraValue, len(in))
	for k, v := range in {
		out[k] = authzv1.ExtraValue(v)
	}

	return out
}
