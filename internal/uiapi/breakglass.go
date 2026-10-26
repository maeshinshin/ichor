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
	"strings"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// BreakGlassVerb is the Kubernetes RBAC verb that lets a user write a
// GitOps-managed (or strict-mode) resource directly in an incident. It is checked per resource with a
// SelfSubjectAccessReview
// made as the user, so Kubernetes decides; Ichor has no list of who may.
const BreakGlassVerb = "break-glass"

// breakGlass is a granted break-glass request, carried on the prPlan that
// planWrite returns: write the CR now, then open the plan's pull request.
type breakGlass struct {
	reason string
	// app and selfHeal describe the tracking Argo CD Application, for the
	// caller's warning. app is empty when nothing tracks the CR yet (a strict
	// approval).
	app      string
	selfHeal bool
}

// withBreakGlass turns a pull request plan into a break-glass plan when the
// request asks for it and the user may. The one place that decides it.
func (s *Server) withBreakGlass(ctx context.Context, obj client.Object, plan *prPlan, bg *pb.BreakGlass) (*prPlan, error) {
	if bg == nil {
		return plan, nil
	}

	reason := strings.TrimSpace(bg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("break-glass needs a reason: it is recorded in the audit record, the Event and the pull request"))
	}

	if err := s.authorizeBreakGlass(ctx, obj); err != nil {
		return nil, err
	}

	plan.bg = &breakGlass{reason: reason, app: plan.app, selfHeal: plan.selfHeal}

	return plan, nil
}

// authorizeBreakGlass asks Kubernetes whether the caller holds the
// break-glass verb on obj. The caller is the OIDC user the backend
// impersonates, or, in the CLI's in-process fallback, whoever the kubeconfig
// is. With neither (the backend on its own ServiceAccount, OIDC off) there is
// nobody to ask, and "cannot tell who" is a refusal.
func (s *Server) authorizeBreakGlass(ctx context.Context, obj client.Object) error {
	_, signedIn := IdentityFrom(ctx)
	if !(signedIn && s.Impersonate != nil) && !s.CallerCredentials {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("break-glass is refused: it needs a signed-in user whose own Kubernetes RBAC grants the "+BreakGlassVerb+
				" verb, and this backend has no user (enable OIDC with ichorUI.oidc, or use `ichor ... --break-glass` with your kubeconfig)"))
	}

	c, err := s.cl(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("break-glass: acting as the user: %w", err))
	}

	_, resource := kindOf(obj)
	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: obj.GetNamespace(), Verb: BreakGlassVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: resource, Name: obj.GetName(),
	}}}

	if err := c.Create(ctx, review); err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("break-glass: asking Kubernetes whether you may: %w", err))
	}

	if !review.Status.Allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not break glass on %s %q: Kubernetes RBAC does not grant you the %q verb on it",
				resource, obj.GetName(), BreakGlassVerb))
	}

	return nil
}

func kindOf(obj client.Object) (kind, resource string) {
	if _, ok := obj.(*ichorv1alpha1.TalosConfigPatch); ok {
		return "TalosConfigPatch", "talosconfigpatches"
	}

	return "TalosMachine", "talosmachines"
}

// afterBreakGlass runs once the direct write has succeeded: it marks audit,
// opens the matching pull request through openPR, emits the Event and returns
// the pull request URL and the notice for the caller. A nil plan or one that
// is not break-glass changes nothing. A failed pull request does not undo the
// write (the cluster is already changed); it is said loudly in the notice.
func (s *Server) afterBreakGlass(
	ctx context.Context, plan *prPlan, obj client.Object, audit *pb.AuditRecord, openPR func() (string, error),
) (url, notice string) {
	if plan == nil || plan.bg == nil {
		return "", ""
	}

	bg := plan.bg
	audit.Action += "-break-glass"
	audit.Reason = bg.reason

	var b strings.Builder

	b.WriteString("Written directly under break-glass. ")

	url, err := openPR()

	switch {
	case err != nil:
		url = ""

		fmt.Fprintf(&b, "The pull request could not be opened (%s): open it by hand, because until it merges Git does not "+
			"match the cluster. ", err.Error())
	default:
		fmt.Fprintf(&b, "Pull request: %s. ", url)
	}

	switch {
	case bg.app == "":
		b.WriteString("Nothing in Argo CD tracks this resource yet; merge the pull request so Git has it.")
	case bg.selfHeal:
		fmt.Fprintf(&b, "Argo CD Application %q has selfHeal on, so this write will be reverted unless the pull request merges "+
			"first: pause selfHeal on that Application in Argo CD until it does (Ichor does not change sync policy).", bg.app)
	default:
		fmt.Fprintf(&b, "Argo CD Application %q does not have selfHeal on, so the change stays until the pull request merges "+
			"(the Application shows OutOfSync meanwhile); if selfHeal is turned on, it will be reverted.", bg.app)
	}

	notice = b.String()

	kind, _ := kindOf(obj)
	msg := fmt.Sprintf("%s %s by %s under break-glass: %s", strings.TrimSuffix(audit.Action, "-break-glass"), obj.GetName(), audit.Actor, bg.reason)

	if err := s.emitBreakGlassEvent(ctx, obj, kind, msg); err != nil {
		notice += fmt.Sprintf(" The Kubernetes Event could not be recorded (%s); the audit record in this response is the only trace.", err.Error())
	}

	return url, notice
}

// emitBreakGlassEvent records a Warning Event on obj. It uses the backend's
// own client (the ServiceAccount, or the CLI user), not the impersonated
// user: users are not given rights to create Events.
func (s *Server) emitBreakGlassEvent(ctx context.Context, obj client.Object, kind, message string) error {
	now := metav1.NewTime(s.now())

	return s.Client.Create(ctx, &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{GenerateName: obj.GetName() + ".", Namespace: obj.GetNamespace()},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: kind,
			Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: obj.GetUID(),
		},
		Reason: "BreakGlass", Message: message, Type: corev1.EventTypeWarning,
		Source:    corev1.EventSource{Component: "ichor-ui"},
		FirstTimestamp: now, LastTimestamp: now, Count: 1,
	})
}
