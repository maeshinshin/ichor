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
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	bgUnitHolder = "holder"
	bgUnitPlain  = "plain"

	bgUnitMachines  = "talosmachines"
	bgUnitFinalizer = "ichor.maesh.dev/machine"
)

var bgUnitResources = []string{bgUnitMachines, "talosconfigpatches"}

// bgUnitHandler builds a handler on a fake client. SubjectAccessReviews are answered by user
// (only bgUnitHolder holds the verb).
func bgUnitHandler(t *testing.T) *OverrideHandler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authzv1.SubjectAccessReview); ok {
				sar.Status.Allowed = sar.Spec.User == bgUnitHolder

				return nil
			}

			return cl.Create(ctx, obj, opts...)
		},
	}).Build()

	return &OverrideHandler{Client: c, ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller")}
}

// bgUnitRequest is an UPDATE of a deleting object; removeFinalizer says whether the new object has
// dropped the Ichor finalizer the stored one carries.
func bgUnitRequest(t *testing.T, resource, user string, removeFinalizer bool) admission.Request {
	t.Helper()

	obj := func(finalizers []string) runtime.RawExtension {
		raw, err := json.Marshal(map[string]any{
			"apiVersion": "ichor.maesh.dev/v1alpha1", "kind": "TalosMachine",
			"metadata": map[string]any{
				"name": "x", "namespace": "default", "finalizers": finalizers,
				"deletionTimestamp": time.Now().UTC().Format(time.RFC3339)},
		})
		if err != nil {
			t.Fatal(err)
		}

		return runtime.RawExtension{Raw: raw}
	}

	next := []string{bgUnitFinalizer}
	if removeFinalizer {
		next = []string{}
	}

	var req admission.Request

	req.Operation, req.Name, req.Namespace = admissionv1.Update, "x", "default"
	req.Resource = metav1.GroupVersionResource{Group: ichorGroup, Version: "v1alpha1", Resource: resource}
	req.UserInfo = authnv1.UserInfo{Username: user}
	req.Object = obj(next)
	req.OldObject = obj([]string{bgUnitFinalizer})

	return req
}

func TestOverrideFinalizerRemoval(t *testing.T) {
	t.Parallel()

	for _, resource := range bgUnitResources {
		h := bgUnitHandler(t)

		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitPlain, true)); r.Allowed {
			t.Errorf("%s: removing an Ichor finalizer without the verb was admitted", resource)
		}

		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitHolder, true)); !r.Allowed {
			t.Errorf("%s: a holder of the verb was refused: %v", resource, r.Result)
		}
	}
}

// A tracked or untracked update that leaves the finalizers alone is nobody's business here.
func TestOverrideOrdinaryUpdatesAreAdmitted(t *testing.T) {
	t.Parallel()

	for _, resource := range bgUnitResources {
		h := bgUnitHandler(t)

		if r := h.Handle(context.Background(), bgUnitRequest(t, resource, bgUnitPlain, false)); !r.Allowed {
			t.Errorf("%s: an ordinary update was refused: %v", resource, r.Result)
		}
	}
}

func TestOverrideExemptUsers(t *testing.T) {
	t.Parallel()

	h := bgUnitHandler(t)

	for _, user := range []string{
		"system:serviceaccount:kube-system:namespace-controller",
		"system:serviceaccount:kube-system:generic-garbage-collector",
		"system:serviceaccount:ichor-system:ichor-controller",
	} {
		for _, resource := range bgUnitResources {
			if r := h.Handle(context.Background(), bgUnitRequest(t, resource, user, true)); !r.Allowed {
				t.Errorf("%s as %s: exempt user refused: %v", resource, user, r.Result)
			}
		}
	}
}
