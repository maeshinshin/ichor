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
	"errors"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/maeshinshin/ichor/pkg/gitopstracking"
)

const (
	bgUnitHolder = "holder"
	bgUnitPlain  = "plain"
	bgUnitArgo   = "system:serviceaccount:argocd:argocd-application-controller"

	bgUnitMachines = "talosmachines"
)

var bgUnitResources = []string{bgUnitMachines, "talosconfigpatches", resClusters}

// bgUnitHandler builds a handler on a fake client. SubjectAccessReviews are answered by user
// (only bgUnitHolder holds the verb); argoErr, when set, makes reading argocd-cm fail.
func bgUnitHandler(t *testing.T, argoErr error) *BreakGlassHandler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cm := &corev1.ConfigMap{
		Name: gitopstracking.ArgoConfigMapName, Namespace: "argocd",
		Data: map[string]string{gitopstracking.ResourceTrackingMethodKey: "annotation"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authzv1.SubjectAccessReview); ok {
				sar.Status.Allowed = sar.Spec.User == bgUnitHolder

				return nil
			}

			return cl.Create(ctx, obj, opts...)
		},
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok && argoErr != nil {
				return argoErr
			}

			return cl.Get(ctx, key, obj, opts...)
		},
	}).Build()

	return &BreakGlassHandler{
		Client: c, ArgoCDNamespace: "argocd",
		ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller", "argocd"),
	}
}

func bgUnitRequest(t *testing.T, resource, user string) admission.Request {
	t.Helper()

	obj := map[string]any{
		"apiVersion": "ichor.maesh.dev/v1alpha1", "kind": "TalosMachine",
		"metadata": map[string]any{
			"name": "x", "namespace": "default",
			"annotations": map[string]any{gitopstracking.ArgoTrackingAnnotation: "app:ichor.maesh.dev/Kind:default/x"},
		},
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}

	return admission.Request{
		Operation: admissionv1.Update, Name: "x", Namespace: "default",
		Resource:  metav1.GroupVersionResource{Group: ichorGroup, Version: "v1alpha1", Resource: resource},
		UserInfo:  authnv1.UserInfo{Username: user},
		Object:    runtime.RawExtension{Raw: raw},
		OldObject: runtime.RawExtension{Raw: raw},
	}
}

func TestBreakGlassTrackedWrites(t *testing.T) {
	t.Parallel()

	for _, resource := range bgUnitResources {
		h := bgUnitHandler(t, nil)

		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitPlain)); r.Allowed {
			t.Errorf("%s: a tracked update without the verb was admitted", resource)
		}

		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitHolder)); !r.Allowed {
			t.Errorf("%s: a holder of the verb was refused: %v", resource, r.Result)
		}
	}
}

func TestBreakGlassExemptUsers(t *testing.T) {
	t.Parallel()

	h := bgUnitHandler(t, errors.New("argocd-cm is forbidden"))

	// Exempt identities are admitted before any lookup, so even a failing argocd-cm cannot stop them.
	for _, user := range []string{
		bgUnitArgo,
		"system:serviceaccount:flux-system:kustomize-controller",
		"system:serviceaccount:flux-system:helm-controller",
		"system:serviceaccount:kube-system:namespace-controller",
		"system:serviceaccount:kube-system:generic-garbage-collector",
		"system:serviceaccount:ichor-system:ichor-controller",
	} {
		for _, resource := range bgUnitResources {
			if r := h.Handle(context.Background(), bgUnitRequest(t, resource, user)); !r.Allowed {
				t.Errorf("%s as %s: exempt user refused: %v", resource, user, r.Result)
			}
		}
	}

	// Exact names only: another account in the Argo CD namespace is not exempt.
	if r := h.Handle(context.Background(), bgUnitRequest(t, bgUnitMachines, "system:serviceaccount:argocd:argocd-server")); r.Allowed {
		t.Error("argocd-server was exempt; the list is exact usernames")
	}
}

func TestBreakGlassTrackingLookupFailureIs503(t *testing.T) {
	t.Parallel()

	h := bgUnitHandler(t, errors.New("argocd-cm is forbidden"))

	for _, resource := range bgUnitResources {
		r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitPlain))
		if r.Allowed || r.Result == nil || r.Result.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: want a refusal with 503, got allowed=%v result=%v", resource, r.Allowed, r.Result)
		}

		// A holder skips the lookup, so an Argo CD outage does not stop a break-glass write.
		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitHolder)); !r.Allowed {
			t.Errorf("%s: holder refused during an Argo CD outage: %v", resource, r.Result)
		}
	}
}
