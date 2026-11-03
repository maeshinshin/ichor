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
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestIchorOperationIsRefusedWhenInstalledByHelm checks ADR 0105: with
// spec.deployment.manager: helm a kind: ichor TalosOperation is refused, whoever asks; with
// manager installer, or no IchorInstallation at all, it is not refused for that reason.
func TestIchorOperationIsRefusedWhenInstalledByHelm(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	installation := func(manager string) *ichorv1alpha1.IchorInstallation {
		return &ichorv1alpha1.IchorInstallation{
			Name: ichorv1alpha1.IchorInstallationName,
			Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.1.0", Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: manager}},
		}
	}

	handler := func(objs ...client.Object) *OverrideHandler {
		return &OverrideHandler{
			Client:      fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
			ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller"),
		}
	}

	request := func(user string) admission.Request {
		op := ichorv1alpha1.TalosOperation{
			Name: "op", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				Kind: ichorv1alpha1.TalosOperationKindIchor, ClusterRef: corev1.LocalObjectReference{Name: "c"},
			},
		}

		raw, err := json.Marshal(op)
		if err != nil {
			t.Fatal(err)
		}

		var req admission.Request

		req.Operation, req.Name, req.Namespace = admissionv1.Create, "op", testNamespace
		req.Resource = metav1.GroupVersionResource{Group: ichorGroup, Version: ichorv1alpha1.GroupVersion.Version, Resource: resOperations}
		req.UserInfo = authnv1.UserInfo{Username: user}
		req.Object = runtime.RawExtension{Raw: raw}

		return req
	}

	for _, user := range []string{bgUnitPlain, testControllerUser} {
		r := handler(installation(ichorv1alpha1.IchorManagerHelm)).Handle(context.Background(), request(user))
		if r.Allowed || r.Result.Message != ichorv1alpha1.IchorHelmManagedMessage {
			t.Errorf("%s on a Helm install: allowed=%v message=%q", user, r.Allowed, r.Result.Message)
		}
	}

	for name, h := range map[string]*OverrideHandler{
		"manager installer": handler(installation(ichorv1alpha1.IchorManagerInstaller)),
		"no installation":   handler(),
	} {
		if r := h.Handle(context.Background(), request(bgUnitPlain)); !r.Allowed {
			t.Errorf("%s: refused: %v", name, r.Result)
		}
	}
}
