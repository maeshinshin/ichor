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
	"testing"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestUIServiceAccountStartsNothing pins what the webhook does with a destructive TalosOperation written
// by ichor-ui's ServiceAccount: it asks whether the ServiceAccount holds the start verb, and nothing else
// gets it through. The ServiceAccount here holds every verb except start, override included, so a refusal
// can only come from the start check. The webhook asks Kubernetes about whoever wrote the object, so it is
// not an independent guard: it passes whatever RBAC grants the writer, which is why the chart must not
// (dist/chart's TestUIServiceAccountHoldsNoStartVerb). ichor-ui writes as the signed-in user, never as
// its ServiceAccount.
func TestUIServiceAccountStartsNothing(t *testing.T) {
	t.Parallel()

	const sa = "system:serviceaccount:ichor-system:ichor-ui"

	h := &OverrideHandler{
		Client: sarClient(t, func(sar *authzv1.SubjectAccessReview) bool {
			return sar.Spec.User == sa && sar.Spec.ResourceAttributes.Verb != ichorv1alpha1.OperationStartVerb
		}),
		ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller"),
	}

	granted := &OverrideHandler{
		Client:      sarClient(t, func(sar *authzv1.SubjectAccessReview) bool { return sar.Spec.User == sa }),
		ExemptUsers: h.ExemptUsers,
	}

	node := func(action string) *ichorv1alpha1.TalosOperationNodeSpec {
		return &ichorv1alpha1.TalosOperationNodeSpec{MachineRef: corev1.LocalObjectReference{Name: "w1"}, Action: action}
	}

	for name, tc := range map[string]struct {
		spec ichorv1alpha1.TalosOperationSpec
		want bool
	}{
		"shutdown":      {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindShutdown}, false},
		"ca-rotation":   {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindCARotation}, false},
		"key-rotation":  {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindKeyRotation}, false},
		"node-shutdown": {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionShutdown)}, false},
		"node-reset":    {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionReset)}, false},
		"etcd-restore":  {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindEtcdRestore}, false},
	} {
		tc.spec.ClusterRef = corev1.LocalObjectReference{Name: "c"}

		if r := h.Handle(context.Background(), createOperationRequest(t, tc.spec, sa)); r.Allowed != tc.want {
			t.Errorf("%s: allowed = %v, want %v (%v)", name, r.Allowed, tc.want, r.Result)
		}

		// The control: given every verb, the same webhook admits the same object, so the refusal above
		// is the missing grant and nothing in the webhook.
		if r := granted.Handle(context.Background(), createOperationRequest(t, tc.spec, sa)); !r.Allowed {
			t.Errorf("%s: refused although the ServiceAccount holds every verb: %v", name, r.Result)
		}
	}
}
