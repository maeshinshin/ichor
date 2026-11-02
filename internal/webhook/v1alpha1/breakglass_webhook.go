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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/deleteguard"
	"github.com/maeshinshin/ichor/pkg/render"
)

// BreakGlassVerb is the RBAC verb that lets a user remove an Ichor finalizer from a deleting
// CR, run a destructive operation or override a per-object guard.
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

// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosmachines;talosconfigpatches,verbs=update,versions=v1alpha1,name=vbreakglass-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosoperations,verbs=create,versions=v1alpha1,name=vbreakglassop-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosclusters,verbs=create;update,versions=v1alpha1,name=vauthentication-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:webhook:path=/validate-ichor-maesh-dev-v1alpha1-breakglass,mutating=false,failurePolicy=fail,sideEffects=None,groups=ichor.maesh.dev,resources=talosetcdbackups,verbs=create;update,versions=v1alpha1,name=vsecretrefs-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create

// BreakGlassOptions configures the break-glass admission check.
type BreakGlassOptions struct {
	// ExemptUsers are the exact usernames never checked: Ichor's own
	// controller and the Kubernetes controllers that cascade deletions
	// (see DefaultExemptUsers).
	ExemptUsers []string
}

// DefaultExemptUsers names the identities whose writes the check must never
// stop, by exact username, never by group or
// namespace.
func DefaultExemptUsers(controllerNamespace, controllerServiceAccount string) []string {
	sa := func(ns, name string) string { return "system:serviceaccount:" + ns + ":" + name }

	users := []string{
		// A namespace or owner deletion cascades into Ichor's CRs.
		sa("kube-system", "namespace-controller"),
		sa("kube-system", "generic-garbage-collector"),
	}
	if controllerNamespace != "" && controllerServiceAccount != "" {
		users = append(users, sa(controllerNamespace, controllerServiceAccount))
	}

	return users
}

// BreakGlassHandler rejects the removal of an ichor.maesh.dev finalizer from
// a deleting CR unless the requesting user holds the break-glass verb on that
// object.
type BreakGlassHandler struct {
	// Client is uncached: it creates SubjectAccessReviews.
	Client      client.Client
	ExemptUsers []string
}

// SetupBreakGlassWebhookWithManager registers the check on the manager's webhook server.
func SetupBreakGlassWebhookWithManager(mgr ctrl.Manager, opts BreakGlassOptions) error {
	direct, err := client.New(mgr.GetConfig(), client.Options{Scheme: mgr.GetScheme()})
	if err != nil {
		return fmt.Errorf("creating the uncached client: %w", err)
	}

	h := &BreakGlassHandler{Client: direct, ExemptUsers: opts.ExemptUsers}
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

	if req.Operation != opUpdate {
		return admission.Allowed("")
	}

	if slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("exempt: Ichor's controller or a Kubernetes cascading-delete controller")
	}

	var old, updated unstructured.Unstructured
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the stored object: %w", err))
	}

	if err := json.Unmarshal(req.Object.Raw, &updated); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the new object: %w", err))
	}

	if !removesIchorFinalizer(&old, &updated) {
		return admission.Allowed("")
	}

	if r := h.require(ctx, req, BreakGlassVerb, req.Resource.Resource, "", req.Name,
		fmt.Sprintf("removing an %s* finalizer from a deleting %s is refused unless you hold the %q verb "+
			"(the controller is still working on it: this orphans a node or an etcd member)", ichorFinalizerPrefix, req.Resource.Resource, BreakGlassVerb)); r != nil {
		return *r
	}

	breakglasslog.Info("write admitted under break-glass", "user", req.UserInfo.Username, "name", req.Name, "operation", req.Operation)

	return admission.Allowed("")
}

// handleOperation guards the dangerous operations, an etcd restore and the node action reset, wherever
// the TalosOperation comes from: the CLI and UI check break-glass too, but a user
// with `create` on talosoperations can write the CR by hand. The
// typed-name confirmation is a CRD rule (confirm == machineRef.name), so it
// needs no copy here.
func (h *BreakGlassHandler) handleOperation(ctx context.Context, req admission.Request) admission.Response {
	if req.Operation != opCreate {
		return admission.Allowed("")
	}

	var op ichorv1alpha1.TalosOperation
	if err := json.Unmarshal(req.Object.Raw, &op); err != nil {
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("decoding the operation: %w", err))
	}

	// There is no Ichor-owned CNI to operate on an unmanaged cluster, for any user.
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindCNI {
		var cluster ichorv1alpha1.TalosCluster

		key := client.ObjectKey{Namespace: req.Namespace, Name: op.Spec.ClusterRef.Name}
		if err := h.Client.Get(ctx, key, &cluster); client.IgnoreNotFound(err) != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("reading cluster %q: %w", key.Name, err))
		} else if err == nil && cluster.Spec.CNI.Preset == presetUnmanaged {
			return admission.Denied(ichorv1alpha1.CNINotManagedMessage)
		}
	}

	// Helm moves an install it owns, never the installer Job, for any user.
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor {
		var inst ichorv1alpha1.IchorInstallation

		err := h.Client.Get(ctx, client.ObjectKey{Name: ichorv1alpha1.IchorInstallationName}, &inst)
		if client.IgnoreNotFound(err) != nil {
			return admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("reading the IchorInstallation: %w", err))
		} else if err == nil && inst.HelmManaged() {
			return admission.Denied(ichorv1alpha1.IchorHelmManagedMessage)
		}
	}

	if slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
	}

	// A destructive start needs the verb start on its subresource, on top of
	// the break-glass requirements below. `create` on talosoperations cannot say which kind it is.
	if sub := op.Spec.StartSubresource(); sub != "" {
		if r := h.require(ctx, req, ichorv1alpha1.OperationStartVerb, "talosoperations", sub, req.Name,
			fmt.Sprintf("a %s operation needs the %q verb on talosoperations/%s", op.Spec.Kind, ichorv1alpha1.OperationStartVerb, sub)); r != nil {
			return *r
		}
	}

	// An etcd restore rewinds the whole cluster to a snapshot: it needs the verb on the cluster.
	if op.Spec.Kind == ichorv1alpha1.TalosOperationKindEtcdRestore {
		cluster := op.Spec.ClusterRef.Name

		if r := h.require(ctx, req, BreakGlassVerb, "talosclusters", "", cluster,
			fmt.Sprintf("an etcd restore rewinds the whole cluster: it needs the %q verb on talosclusters/%s", BreakGlassVerb, cluster)); r != nil {
			return *r
		}

		breakglasslog.Info("etcd restore admitted under break-glass", "user", req.UserInfo.Username, "cluster", cluster)

		return admission.Allowed("")
	}

	if op.Spec.Kind != ichorv1alpha1.TalosOperationKindNode || op.Spec.Node == nil {
		return admission.Allowed("")
	}

	// A reset wipes the node; a breakGlassReason on any other action overrides the last-worker guard.
	// Either needs the verb on the machine.
	reset := op.Spec.Node.Action == ichorv1alpha1.NodeActionReset
	if !reset && op.Spec.Node.BreakGlassReason == "" {
		return admission.Allowed("")
	}

	machine := op.Spec.Node.MachineRef.Name

	what := "a reset wipes the node"
	if !reset {
		what = "spec.node.breakGlassReason overrides the last-worker guard"
	}

	if r := h.require(ctx, req, BreakGlassVerb, "talosmachines", "", machine,
		fmt.Sprintf("%s: it needs the %q verb on talosmachines/%s", what, BreakGlassVerb, machine)); r != nil {
		return *r
	}

	breakglasslog.Info("node action admitted under break-glass", "user", req.UserInfo.Username, "machine", machine, "action", op.Spec.Node.Action)

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

// require returns nil when the requesting user holds verb on resource (and subresource, when set),
// else the 503 for a failed check or a denial with the given message.
func (h *BreakGlassHandler) require(ctx context.Context, req admission.Request, verb, resource, subresource, name, denied string) *admission.Response {
	allowed, err := h.mayDo(ctx, req.UserInfo, verb, resource, subresource, name, req.Namespace)

	var r admission.Response

	switch {
	case err != nil:
		r = admission.Errored(http.StatusServiceUnavailable, fmt.Errorf("cannot check the %s right: %w", verb, err))
	case !allowed:
		r = admission.Denied(denied)
	default:
		return nil
	}

	return &r
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
// TalosCluster.spec.authentication: verb update on talosclusters/authentication.
const (
	AuthenticationVerb        = "update"
	AuthenticationSubresource = "authentication"
)

// handleCluster gates a change to spec.authentication. An operator who could repoint the IdP could
// break sign-in for everyone, so the change needs `update` on talosclusters/authentication (asked as
// the requesting user, so a hand-written CR obeys it too), and the new value must be one the
// kube-apiserver will accept: the contract is v1.14 or later and the configuration it renders passes
// the apiserver's own validation. A write that leaves spec.authentication as it was is not checked,
// and the controller (ExemptUsers) is never stopped.
func (h *BreakGlassHandler) handleCluster(ctx context.Context, req admission.Request) admission.Response {
	if slices.Contains(h.ExemptUsers, req.UserInfo.Username) {
		return admission.Allowed("")
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

	if resp := h.gateSecretReferences(ctx, req, "talosclusters", clusterSecretRefChanges(&old, &updated)); resp != nil {
		return *resp
	}

	if reflect.DeepEqual(old.Spec.Authentication, updated.Spec.Authentication) {
		return admission.Allowed("")
	}

	if r := h.require(ctx, req, AuthenticationVerb, "talosclusters", AuthenticationSubresource, req.Name,
		fmt.Sprintf("changing spec.authentication needs the %q verb on talosclusters/%s: "+
			"it decides who can sign in to the Kubernetes API", AuthenticationVerb, AuthenticationSubresource)); r != nil {
		return *r
	}

	if err := render.ValidateAuthentication(&updated); err != nil {
		return admission.Denied(err.Error())
	}

	breakglasslog.Info("spec.authentication changed", "user", req.UserInfo.Username, "cluster", req.Name)

	return admission.Allowed("")
}

// SecretReferencesVerb and SecretReferencesSubresource are the RBAC right that gates setting or
// changing a field naming a Secret the controller (or the UI backend) reads with its own, broader
// privileges: verb update on <resource>/secret-references.
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

	// A registry's credential goes to its endpoints, so a changed endpoint list counts as much as a
	// changed Secret name. hadPair remembers the (host, Secret) pairs to tell the two apart.
	had, hadPair := map[string]bool{}, map[string]bool{}

	for _, r := range old.Spec.Registries {
		if r.AuthSecretRef != nil {
			pair := r.Host + "\x00" + r.AuthSecretRef.Name
			hadPair[pair] = true
			had[pair+"\x00"+strings.Join(r.Endpoints, "\x00")] = true
		}
	}

	for _, r := range updated.Spec.Registries {
		if r.AuthSecretRef == nil {
			continue
		}

		pair := r.Host + "\x00" + r.AuthSecretRef.Name
		if had[pair+"\x00"+strings.Join(r.Endpoints, "\x00")] {
			continue
		}

		if hadPair[pair] {
			changed = append(changed, "spec.registries[].endpoints")
		} else {
			changed = append(changed, "spec.registries[].authSecretRef")
		}

		break
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

	if r := h.require(ctx, req, SecretReferencesVerb, resource, SecretReferencesSubresource, req.Name,
		fmt.Sprintf("setting or changing %s needs the %q verb on %s/%s: "+
			"the Secret it names is read with Ichor's own privileges", strings.Join(changed, ", "), SecretReferencesVerb, resource, SecretReferencesSubresource)); r != nil {
		return r
	}

	breakglasslog.Info("secret reference changed", "user", req.UserInfo.Username, "resource", resource, "name", req.Name, "fields", changed)

	return nil
}

// BreakGlassCheck tells whether the user behind the admission request in ctx holds the break-glass
// verb on resource/name. The per-object guards (the last-worker guard) take one so
// an operator with the verb can override them; nil means no override.
type BreakGlassCheck func(ctx context.Context, resource, name, namespace string) (bool, error)

// RightCheck tells whether the user behind the admission request in ctx holds verb on resource
// (and subresource, when set). Outside an admission request there is no user, so the answer is no.
type RightCheck func(ctx context.Context, verb, resource, subresource, name, namespace string) (bool, error)

// rightCheckFor asks Kubernetes with a SubjectAccessReview on the requesting user.
func rightCheckFor(c client.Client) RightCheck {
	h := &BreakGlassHandler{Client: c}

	return func(ctx context.Context, verb, resource, subresource, name, namespace string) (bool, error) {
		req, err := admission.RequestFromContext(ctx)
		if err != nil {
			return false, nil //nolint:nilerr // no admission request, no user to hold the verb
		}

		return h.mayDo(ctx, req.UserInfo, verb, resource, subresource, name, namespace)
	}
}

// breakGlassCheckFor asks Kubernetes with a SubjectAccessReview on the requesting user, like the
// break-glass handler does. Outside an admission request there is no user, so the answer is no.
func breakGlassCheckFor(c client.Client) BreakGlassCheck {
	h := &BreakGlassHandler{Client: c}

	return func(ctx context.Context, resource, name, namespace string) (bool, error) {
		req, err := admission.RequestFromContext(ctx)
		if err != nil {
			return false, nil //nolint:nilerr // no admission request, no user to hold the verb
		}

		return h.mayDo(ctx, req.UserInfo, BreakGlassVerb, resource, "", name, namespace)
	}
}

// checkLastWorker is deleteguard.CheckLastWorker with the break-glass override: it asks the verb only
// when the guard would refuse, so ordinary requests cost no SubjectAccessReview. A failed check
// refuses (fail closed).
func (c BreakGlassCheck) checkLastWorker(ctx context.Context, workersAfter int, resource, name, namespace string) error {
	err := deleteguard.CheckLastWorker(true, workersAfter, false)
	if err == nil || c == nil {
		return err
	}

	allowed, aerr := c(ctx, resource, name, namespace)
	if aerr != nil {
		return fmt.Errorf("%w (and the %s right could not be checked: %w)", err, BreakGlassVerb, aerr)
	}

	if !allowed {
		return err
	}

	breakglasslog.Info("last-worker guard overridden under break-glass", "resource", resource, "name", name)

	return deleteguard.CheckLastWorker(true, workersAfter, true)
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
