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

// TestOpenModeServiceAccountStarts pins which of the six destructive starts ichor-ui's
// ServiceAccount gets through when the chart gives it the start verb on all six subresources and
// nothing else: the four that ask only for start, not the two that ask for override as well. The
// ServiceAccount is the caller only with --allow-unauthenticated.
func TestOpenModeServiceAccountStarts(t *testing.T) {
	t.Parallel()

	const sa = "system:serviceaccount:ichor-system:ichor-ui"

	startSubresources := map[string]bool{
		"shutdown": true, "ca-rotation": true, "key-rotation": true, "etcd-restore": true, "node-reset": true, "node-shutdown": true,
	}

	h := &OverrideHandler{
		Client: sarClient(t, func(sar *authzv1.SubjectAccessReview) bool {
			ra := sar.Spec.ResourceAttributes

			return sar.Spec.User == sa && ra.Verb == ichorv1alpha1.OperationStartVerb && ra.Resource == resOperations && startSubresources[ra.Subresource]
		}),
		ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller"),
	}

	node := func(action string) *ichorv1alpha1.TalosOperationNodeSpec {
		return &ichorv1alpha1.TalosOperationNodeSpec{MachineRef: corev1.LocalObjectReference{Name: "w1"}, Action: action}
	}

	for name, tc := range map[string]struct {
		spec ichorv1alpha1.TalosOperationSpec
		want bool
	}{
		"shutdown":      {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindShutdown}, true},
		"ca-rotation":   {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindCARotation}, true},
		"key-rotation":  {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindKeyRotation}, true},
		"node-shutdown": {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionShutdown)}, true},
		"node-reset":    {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindNode, Node: node(ichorv1alpha1.NodeActionReset)}, false},
		"etcd-restore":  {ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindEtcdRestore}, false},
	} {
		tc.spec.ClusterRef = corev1.LocalObjectReference{Name: "c"}

		if r := h.Handle(context.Background(), createOperationRequest(t, tc.spec, sa)); r.Allowed != tc.want {
			t.Errorf("%s: allowed = %v, want %v (%v)", name, r.Allowed, tc.want, r.Result)
		}
	}
}
