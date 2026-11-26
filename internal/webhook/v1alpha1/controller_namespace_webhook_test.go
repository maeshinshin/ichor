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
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const controllerNS = "ichor-system"

func nsRequest(op admissionv1.Operation, resource, namespace, user string) admission.Request {
	return admission.Request{
		Operation: op, Namespace: namespace, Name: "x",
		Resource: metav1.GroupVersionResource{Group: ichorGroup, Version: "v1alpha1", Resource: resource},
		UserInfo: authenticationv1.UserInfo{Username: user},
	}
}

// A new Ichor object outside the controller's namespace is refused, naming that namespace; the same create
// inside is admitted, and an update or a delete of an object already outside is never refused.
func TestControllerNamespaceWebhook(t *testing.T) {
	h := &ControllerNamespaceHandler{Namespace: controllerNS, ExemptUsers: []string{"controller"}}

	for _, c := range []struct {
		name    string
		req     admission.Request
		allowed bool
	}{
		{"create outside", nsRequest(admissionv1.Create, "talosclusters", "tenant", "alice"), false},
		{"create outside, another kind", nsRequest(admissionv1.Create, "ichorusers", "default", "alice"), false},
		{"create inside", nsRequest(admissionv1.Create, "talosclusters", controllerNS, "alice"), true},
		{"update outside", nsRequest(admissionv1.Update, "talosclusters", "tenant", "alice"), true},
		{"delete outside", nsRequest(admissionv1.Delete, "talosclusters", "tenant", "alice"), true},
		{"controller creates outside", nsRequest(admissionv1.Create, "talosmachines", "tenant", "controller"), true},
	} {
		got := h.Handle(t.Context(), c.req)
		if got.Allowed != c.allowed {
			t.Errorf("%s: allowed = %v, want %v (%v)", c.name, got.Allowed, c.allowed, got.Result)
		}

		if !c.allowed && (got.Result == nil || !strings.Contains(got.Result.Message, controllerNS)) {
			t.Errorf("%s: refusal %v does not name %q", c.name, got.Result, controllerNS)
		}
	}
}
