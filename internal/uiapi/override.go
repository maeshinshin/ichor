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

package uiapi

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// OverrideVerb is the Kubernetes RBAC verb that lets a user run a destructive
// operation or override a guard in an incident (docs/adr/0065). It is checked per resource with a SelfSubjectAccessReview
// made as the user, so Kubernetes decides; Ichor has no list of who may.
const OverrideVerb = "override"

const (
	kindTalosMachine = "TalosMachine"
	kindTalosCluster = "TalosCluster"
)

// authorizeOverride asks Kubernetes whether the caller holds the
// override verb on obj. The caller is the OIDC user the backend
// impersonates, or, in the CLI's in-process fallback, whoever the kubeconfig
// is. With neither (the backend on its own ServiceAccount, OIDC off) there is
// nobody to ask, and "cannot tell who" is a refusal.
func (s *Server) authorizeOverride(ctx context.Context, obj client.Object) error {
	_, signedIn := IdentityFrom(ctx)
	if (!signedIn || s.Impersonate == nil) && !s.CallerCredentials {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("override is refused: it needs a signed-in user whose own Kubernetes RBAC grants the "+OverrideVerb+
				" verb, and this backend has no user (enable OIDC with ichorUI.oidc, or use `ichor ... --override` with your kubeconfig)"))
	}

	c, err := s.cl(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("override: acting as the user: %w", err))
	}

	_, resource := kindOf(obj)
	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: obj.GetNamespace(), Verb: OverrideVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: resource, Name: obj.GetName(),
	}}}

	if err := c.Create(ctx, review); err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("override: asking Kubernetes whether you may: %w", err))
	}

	if !review.Status.Allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not override on %s %q: Kubernetes RBAC does not grant you the %q verb on it",
				resource, obj.GetName(), OverrideVerb))
	}

	return nil
}

func kindOf(obj client.Object) (kind, resource string) {
	if _, ok := obj.(*ichorv1alpha1.TalosConfigPatch); ok {
		return "TalosConfigPatch", "talosconfigpatches"
	}

	if _, ok := obj.(*ichorv1alpha1.TalosCluster); ok {
		return kindTalosCluster, "talosclusters"
	}

	return kindTalosMachine, "talosmachines"
}

// emitOverrideEvent records a Warning Event on obj. It uses the backend's
// own client (the ServiceAccount, or the CLI user), not the impersonated
// user: users are not given rights to create Events.
func (s *Server) emitOverrideEvent(ctx context.Context, obj client.Object, kind, message string) error {
	return s.emitWarningEvent(ctx, obj, kind, "Override", message)
}

// emitWarningEvent records a Warning Event with reason on obj, as the backend's own client.
func (s *Server) emitWarningEvent(ctx context.Context, obj client.Object, kind, reason, message string) error {
	now := metav1.NewTime(s.now())

	ev := &corev1.Event{
		InvolvedObject: corev1.ObjectReference{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: kind,
			Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: obj.GetUID(),
		},
		Reason: reason, Message: message, Type: corev1.EventTypeWarning,
		Source:         corev1.EventSource{Component: "ichor-ui"},
		FirstTimestamp: now, LastTimestamp: now, Count: 1,
	}
	ev.GenerateName, ev.Namespace = obj.GetName()+".", obj.GetNamespace()

	return s.Client.Create(ctx, ev)
}

const (
	fieldRole  = "role"
	verbUpdate = "update"
)
