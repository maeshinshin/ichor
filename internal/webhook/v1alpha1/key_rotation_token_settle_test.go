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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestKeyRotationWithATokenSettleShorterThanTheKubeletRefreshIsRefused checks the hardware amendment of ADR 0121:
// dropping the old service-account key before pods have refreshed their projected tokens locks Ichor's own
// controller and the CNI out, so a shorter spec.keyRotation.tokenSettle is refused whoever asks, the controller's
// own user included; only the service-account key has tokens.
func TestKeyRotationWithATokenSettleShorterThanTheKubeletRefreshIsRefused(t *testing.T) {
	t.Parallel()

	h := &OverrideHandler{
		Client:      fake.NewClientBuilder().WithScheme(schemeOf(t)).Build(),
		ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller"),
	}

	request := func(user string, targets []string, settle time.Duration) admission.Request {
		return createOperationRequest(t, ichorv1alpha1.TalosOperationSpec{
			Kind: ichorv1alpha1.TalosOperationKindKeyRotation, ClusterRef: corev1.LocalObjectReference{Name: "c"},
			KeyRotation: &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: targets, TokenSettle: &metav1.Duration{Duration: settle}},
		}, user)
	}

	both := []string{ichorv1alpha1.KeyRotationTargetServiceAccount, ichorv1alpha1.KeyRotationTargetSecretbox}

	for _, user := range []string{bgUnitPlain, testControllerUser} {
		r := h.Handle(context.Background(), request(user, both, 5*time.Minute))
		if r.Allowed || !strings.Contains(r.Result.Message, "tokenSettle 5m0s") || !strings.Contains(r.Result.Message, "kubelet") {
			t.Errorf("%s with tokenSettle 5m: allowed=%v message=%q", user, r.Allowed, r.Result.Message)
		}
	}

	// The exempt user stands for the callers the SubjectAccessReview does not apply to: long enough, or no tokens.
	for name, req := range map[string]admission.Request{
		"the minimum":        request(testControllerUser, both, ichorv1alpha1.MinKeyRotationTokenSettle),
		"a long wait":        request(testControllerUser, both, 3*time.Hour),
		"secretbox only, 5m": request(testControllerUser, []string{ichorv1alpha1.KeyRotationTargetSecretbox}, 5*time.Minute),
	} {
		if r := h.Handle(context.Background(), req); !r.Allowed {
			t.Errorf("%s: refused: %v", name, r.Result)
		}
	}
}
