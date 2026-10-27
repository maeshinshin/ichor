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
	"reflect"
	"slices"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
	"github.com/maeshinshin/ichor/pkg/render"
)

// BreakGlassVerb is the RBAC verb that lets a user write a GitOps-managed CR
// directly, or remove an Ichor finalizer from a deleting one (ADR 0065).
const BreakGlassVerb = "break-glass"

const (
	opCreate = "CREATE"
	opUpdate = "UPDATE"
)

const (
	breakGlassWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-breakglass"
	ichorFinalizerPrefix  = "ichor.maesh.dev/"
	ichorGroup            = "ichor.maesh.dev"
)

var breakglasslog = logf.Log.WithName("breakglass-webhook")

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines;talosconfigpatches,verbs=update;delete,versions=v1alpha1,name=vbreakglass-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosoperations,verbs=create,versions=v1alpha1,name=vbreakglassop-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=create;update;delete,versions=v1alpha1,name=vauthentication-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=create;update,versions=v1alpha1,name=vsecretrefs-v1alpha1.kb.io,admissionReviewVersions=v1
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
// stop (ADR 0065, decision 8): by exact username, never by group or
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
	switch req.Resource.Resource {
	case "talosoperations":
		return h.handleOperation(ctx, req)
	case "talosclusters":
		return h.handleCluster(ctx, req)
	case "talosetcdbackups":
		return h.handleEtcdBackup(ctx, req)
	}

	if req.Operation != opUpdate && req.Operation != "DELETE" {
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

	if req.Operation == opUpdate {
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

	if resp := h.refuseIfTracked(ctx, req, &old); resp != nil {
		return *resp
	}

	return admission.Allowed("")
}

// refuseIfTracked returns a denial when obj (the stored object) is tracked by Argo CD or Flux, and
// a 503 when that cannot be told: ADR 0044, do not write. Argo CD and Flux are exempt before this
// is reached, so refusing here never blocks a sync. nil means "not tracked, carry on".
func (h *BreakGlassHandler) refuseIfTracked(ctx context.Context, req admission.Request, obj client.Object) *admission.Response {
	tracked, by, err := gitopstracking.IsTracked(ctx, h.Client, h.ArgoCDNamespace, obj)
	if err != nil {
		r := admission.Errored(http.StatusServiceUnavailable,
			fmt.Errorf("cannot tell whether %s is managed by GitOps, so the direct write is refused: %w", req.Name, err))

		return &r
	}

	if tracked {
		r := admission.Denied(fmt.Sprintf("%s %q is managed by %s: change it in Git, or hold the %q verb to write it directly in an incident",
			req.Resource.Resource, req.Name, by, BreakGlassVerb))

		return &r
	}

	return nil
}

// handleOperation guards the dangerous operations, an etcd restore and the node action reset, wherever
// the TalosOperation comes from: the CLI and UI check break-glass too, but a user
// with `create` on talosoperations can write the CR by hand (ADR 0085). The
// typed-name confirmation is a CRD rule (confirm == machineRef.name), so it
// needs no copy here.
func (h *BreakGlassHandler) handleOperation(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != opCreate || slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
	}

	var op ichorv1alpha1.TalosOperation
	if err := json.Unmarshal(req.Object.Raw, &op); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the operation: %w", err))
	}

	// A destructive start needs the verb start on its subresource (ADR 0094, Decision 4), on top of
	// the break-glass requirements below. `create` on talosoperations cannot say which kind it is.
	if sub := op.Spec.StartSubresource(); sub != "" {
		allowed, err := h.mayDo(ctx, req.UserInfo, ichorv1alpha1.OperationStartVerb, "talosoperations", sub, req.Name, req.Namespace)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", ichorv1alpha1.OperationStartVerb, err))
		}

		if !allowed {
			return admission.Denied(fmt.Sprintf("a %s operation needs the %q verb on talosoperations/%s",
				op.Spec.Kind, ichorv1alpha1.OperationStartVerb, sub))
		}
	}

	// An etcd restore rewinds the whole cluster to a snapshot: it needs the verb on the cluster (ADR 0089).
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindEtcdRestore {
		cluster := op.Spec.ClusterRef.Name

		allowed, err := h.mayBreakGlass(ctx, req.UserInfo, "talosclusters", cluster, req.Namespace)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", BreakGlassVerb, err))
		}

		if !allowed {
			return admission.Denied(fmt.Sprintf("an etcd restore rewinds the whole cluster: it needs the %q verb on talosclusters/%s", BreakGlassVerb, cluster))
		}

		breakglasslog.Info("etcd restore admitted under break-glass", "user", req.UserInfo.Username, "cluster", cluster)

		return admission.Allowed("")
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
	return h.mayDo(ctx, user, BreakGlassVerb, resource, "", name, namespace)
}

// mayDo asks Kubernetes, with a SubjectAccessReview on the requesting user, whether they hold
// verb on resource (and subresource, when set).
func (h *BreakGlassHandler) mayDo(ctx context.Context, user authnv1.UserInfo, verb, resource, subresource, name, namespace string) (bool, error) {
	sar := &authzv1.SubjectAccessReview{
		Spec: authzv1.SubjectAccessReviewSpec{
			User:   user.Username,
			UID:    user.UID,
			Groups: user.Groups,
			Extra:  extraOf(user.Extra),
			ResourceAttributes: &authzv1.ResourceAttributes{
				Group:       ichorGroup,
				Resource:    resource,
				Subresource: subresource,
				Verb:        verb,
				Name:        name,
				Namespace:   namespace,
			},
		},
	}

	if err := h.Client.Create(ctx, sar); err != nil {
		return false, err
	}

	return sar.Status.Allowed, nil
}

// AuthenticationVerb and AuthenticationSubresource are the RBAC right that gates a change to
// TalosCluster.spec.authentication (ADR 0094, Decision 4): verb update on talosclusters/authentication.
const (
	AuthenticationVerb        = "update"
	AuthenticationSubresource = "authentication"
)

// handleClusterDelete refuses deleting a tracked TalosCluster unless the user holds break-glass on it.
func (h *BreakGlassHandler) handleClusterDelete(ctx context.Context, req admission.Request) admission.Response {
	var old ichorv1alpha1.TalosCluster
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored cluster: %w", err))
	}

	allowed, err := h.mayBreakGlass(ctx, req.UserInfo, "talosclusters", req.Name, req.Namespace)
	if err != nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", BreakGlassVerb, err))
	}

	if allowed {
		breakglasslog.Info("cluster delete admitted under break-glass", "user", req.UserInfo.Username, "cluster", req.Name)

		return admission.Allowed("")
	}

	if resp := h.refuseIfTracked(ctx, req, &old); resp != nil {
		return *resp
	}

	return admission.Allowed("")
}

// handleCluster gates a change to spec.authentication. An operator who could repoint the IdP could
// break sign-in for everyone, so the change needs `update` on talosclusters/authentication (asked as
// the requesting user, so a hand-written CR obeys it too), and the new value must be one the
// kube-apiserver will accept: the contract is v1.14 or later and the configuration it renders passes
// the apiserver's own validation. A write that leaves spec.authentication as it was is not checked,
// and the controller and GitOps engines (ExemptUsers) are never stopped.
func (h *BreakGlassHandler) handleCluster(ctx context.Context, req admission.Request) admission.Response {
	if slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
	}

	// Deleting a GitOps-tracked cluster needs the break-glass verb too (ADR 0065); the allow-delete
	// guard is a separate webhook and stays as it is.
	if req.Operation == "DELETE" {
		return h.handleClusterDelete(ctx, req)
	}

	if req.Operation != opCreate && req.Operation != opUpdate {
		return admission.Allowed("")
	}

	var updated ichorv1alpha1.TalosCluster
	if err := json.Unmarshal(req.Object.Raw, &updated); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the cluster: %w", err))
	}

	var old ichorv1alpha1.TalosCluster

	if req.Operation == opUpdate {
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored cluster: %w", err))
		}
	}

	// A direct update of a GitOps-tracked cluster needs the break-glass verb like a machine or a patch
	// (ADR 0065). Holders skip the tracking lookup; the gates below still apply to them.
	if req.Operation == opUpdate {
		allowed, err := h.mayBreakGlass(ctx, req.UserInfo, "talosclusters", req.Name, req.Namespace)
		if err != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", BreakGlassVerb, err))
		}

		if !allowed {
			if resp := h.refuseIfTracked(ctx, req, &old); resp != nil {
				return *resp
			}
		}
	}

	if resp := h.gateSecretReferences(ctx, req, "talosclusters", clusterSecretRefChanges(&old, &updated)); resp != nil {
		return *resp
	}

	if reflect.DeepEqual(old.Spec.Authentication, updated.Spec.Authentication) {
		return admission.Allowed("")
	}

	allowed, err := h.mayDo(ctx, req.UserInfo, AuthenticationVerb, "talosclusters", AuthenticationSubresource, req.Name, req.Namespace)
	if err != nil {
		return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", AuthenticationVerb, err))
	}

	if !allowed {
		return admission.Denied(fmt.Sprintf("changing spec.authentication needs the %q verb on talosclusters/%s: "+
			"it decides who can sign in to the Kubernetes API", AuthenticationVerb, AuthenticationSubresource))
	}

	if err := render.ValidateAuthentication(&updated); err != nil {
		return admission.Denied(err.Error())
	}

	breakglasslog.Info("spec.authentication changed", "user", req.UserInfo.Username, "cluster", req.Name)

	return admission.Allowed("")
}

// SecretReferencesVerb and SecretReferencesSubresource are the RBAC right that gates setting or
// changing a field naming a Secret the controller (or the UI backend) reads with its own, broader
// privileges (ADR 0096): verb update on <resource>/secret-references.
const (
	SecretReferencesVerb        = "update"
	SecretReferencesSubresource = "secret-references"
)

// clusterSecretRefChanges lists the Secret references updated sets or changes relative to old
// (the zero value on create). A reference that is removed, or kept as it was, is not listed.
func clusterSecretRefChanges(old, updated *ichorv1alpha1.TalosCluster) []string {
	var changed []string

	if updated.Spec.SecretsRef != old.Spec.SecretsRef {
		changed = append(changed, "spec.secretsRef")
	}

	var before, after *corev1.SecretKeySelector
	if r := old.Spec.GitOps; r != nil && r.Repository != nil {
		before = &r.Repository.TokenSecretRef
	}

	if r := updated.Spec.GitOps; r != nil && r.Repository != nil {
		after = &r.Repository.TokenSecretRef
	}

	if after != nil && (before == nil || !reflect.DeepEqual(*before, *after)) {
		changed = append(changed, "spec.gitops.repository.tokenSecretRef")
	}

	had := map[string]bool{}

	for _, r := range old.Spec.Registries {
		if r.AuthSecretRef != nil {
			had[r.Host+"\x00"+r.AuthSecretRef.Name] = true
		}
	}

	for _, r := range updated.Spec.Registries {
		if r.AuthSecretRef != nil && !had[r.Host+"\x00"+r.AuthSecretRef.Name] {
			changed = append(changed, "spec.registries[].authSecretRef")

			break
		}
	}

	return changed
}

// handleEtcdBackup gates spec.destination.s3.credentialsSecretRef the same way.
func (h *BreakGlassHandler) handleEtcdBackup(ctx context.Context, req admission.Request) admission.Response {
	if (req.Operation != opCreate && req.Operation != opUpdate) || slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
	}

	var updated, old ichorv1alpha1.TalosEtcdBackup
	if err := json.Unmarshal(req.Object.Raw, &updated); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the backup: %w", err))
	}

	if req.Operation == opUpdate {
		if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored backup: %w", err))
		}
	}

	var changed []string

	if updated.Spec.Destination.S3.CredentialsSecretRef != old.Spec.Destination.S3.CredentialsSecretRef {
		changed = append(changed, "spec.destination.s3.credentialsSecretRef")
	}

	if resp := h.gateSecretReferences(ctx, req, "talosetcdbackups", changed); resp != nil {
		return *resp
	}

	return admission.Allowed("")
}

// gateSecretReferences returns a denial (or an error) unless changed is empty or the requesting
// user holds update on <resource>/secret-references. nil means "carry on".
func (h *BreakGlassHandler) gateSecretReferences(ctx context.Context, req admission.Request, resource string, changed []string) *admission.Response {
	if len(changed) == 0 {
		return nil
	}

	allowed, err := h.mayDo(ctx, req.UserInfo, SecretReferencesVerb, resource, SecretReferencesSubresource, req.Name, req.Namespace)
	if err != nil {
		r := admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", SecretReferencesVerb, err))

		return &r
	}

	if !allowed {
		r := admission.Denied(fmt.Sprintf("setting or changing %s needs the %q verb on %s/%s: "+
			"the Secret it names is read with Ichor's own privileges", strings.Join(changed, ", "), SecretReferencesVerb, resource, SecretReferencesSubresource))

		return &r
	}

	breakglasslog.Info("secret reference changed", "user", req.UserInfo.Username, "resource", resource, "name", req.Name, "fields", changed)

	return nil
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
