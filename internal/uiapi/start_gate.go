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
	"fmt"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// authorizeStart asks Kubernetes, as the caller, whether they hold the verb start on the
// talosoperations subresource of spec's kind (ADR 0094, Decision 4): a shutdown, a CA rotation, an
// etcd restore, or the node actions reset and shutdown. `create` on talosoperations cannot say which
// kind is being created, so this is a separate right. The validating webhook asks the same question
// for a hand-written TalosOperation; asking here gives the caller the refusal before anything is
// written, and before the override questions.
//
// With nobody to ask (the backend on its own ServiceAccount, OIDC off) it asks nothing: the webhook
// then judges the ServiceAccount, which the chart grants the verb.
func (s *Server) authorizeStart(ctx context.Context, spec *ichorv1alpha1.TalosOperationSpec, namespace string) error {
	sub := spec.StartSubresource()
	if sub == "" {
		return nil
	}

	_, signedIn := IdentityFrom(ctx)
	if (!signedIn || s.Impersonate == nil) && !s.CallerCredentials {
		return nil
	}

	c, err := s.cl(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("start: acting as the user: %w", err))
	}

	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: namespace, Verb: ichorv1alpha1.OperationStartVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: "talosoperations", Subresource: sub,
	}}}

	if err := c.Create(ctx, review); err != nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("start: asking Kubernetes whether you may: %w", err))
	}

	if !review.Status.Allowed {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("you may not start a %s operation: Kubernetes RBAC does not grant you the %q verb on talosoperations/%s",
				spec.Kind, ichorv1alpha1.OperationStartVerb, sub))
	}

	return nil
}
