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
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Who may ask the controller for which certificate. The SubjectAccessReview the webhook makes
// is asked as the requester, on the permission of the purpose.
func TestCredentialRequestWebhook(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	machine := &ichorv1alpha1.TalosMachine{
		ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns"},
		Spec:       ichorv1alpha1.TalosMachineSpec{ClusterRef: corev1.LocalObjectReference{Name: "c1"}},
	}

	// grants maps a requester to the "verb resource/subresource" strings it holds.
	grants := map[string][]string{
		"sa":     {"issue talosclusters/public", "issue talosclusters/node-reader"},
		"dl":     {"download talosmachines/etcd-snapshot", "download talosclusters/credentials"},
		"nobody": nil,
	}

	var asked []string

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(machine).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			sar, ok := obj.(*authzv1.SubjectAccessReview)
			if !ok {
				return cl.Create(ctx, obj, opts...)
			}

			ra := sar.Spec.ResourceAttributes
			want := ra.Verb + " " + ra.Resource + "/" + ra.Subresource
			asked = append(asked, sar.Spec.User+": "+want+" "+ra.Name)

			for _, g := range grants[sar.Spec.User] {
				if g == want {
					sar.Status.Allowed = true
				}
			}

			return nil
		},
	}).Build()

	h := &CredentialRequestHandler{Client: c}

	handle := func(user string, spec ichorv1alpha1.TalosCredentialRequestSpec) admission.Response {
		asked = nil
		spec.ClusterRef = corev1.LocalObjectReference{Name: "c1"}

		raw, err := json.Marshal(ichorv1alpha1.TalosCredentialRequest{Spec: spec})
		if err != nil {
			t.Fatal(err)
		}

		req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
			Operation: admissionv1.Create, Namespace: "ns", UserInfo: authnv1.UserInfo{Username: user},
			Object: runtime.RawExtension{Raw: raw},
		}}

		return h.Handle(context.Background(), req)
	}

	m1 := &corev1.LocalObjectReference{Name: "m1"}

	tests := []struct {
		name    string
		user    string
		spec    ichorv1alpha1.TalosCredentialRequestSpec
		allowed bool
	}{
		{"the backend reads public data", "sa", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposePublic}, true},
		{"the backend asks for its reader certificate", "sa", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeNodeReader}, true},
		{"a user cannot get the reader certificate", "dl", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeNodeReader}, false},
		{"the backend cannot get admin credentials", "sa", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeCredentialsTalos}, false},
		{"a downloader gets the etcd snapshot certificate", "dl", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeEtcdSnapshot, MachineRef: m1}, true},
		{"a downloader gets no packet capture certificate", "dl", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposePacketCapture, MachineRef: m1}, false},
		{"a downloader gets the operator credentials", "dl", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeCredentialsKubernetes}, true},
		{"nobody gets anything", "nobody", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeCredentialsTalos}, false},
		{"a download needs a machine", "dl", ichorv1alpha1.TalosCredentialRequestSpec{Purpose: ichorv1alpha1.CredentialPurposeEtcdSnapshot}, false},
		{"a machine of another cluster", "dl", ichorv1alpha1.TalosCredentialRequestSpec{
			Purpose: ichorv1alpha1.CredentialPurposeEtcdSnapshot, MachineRef: &corev1.LocalObjectReference{Name: "missing"}}, false},
	}

	for _, tt := range tests {
		if got := handle(tt.user, tt.spec); got.Allowed != tt.allowed {
			t.Errorf("%s: allowed = %v, want %v (%v; asked %v)", tt.name, got.Allowed, tt.allowed, got.Result, asked)
		}
	}

	if got := h.Handle(context.Background(), admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{Operation: admissionv1.Update}}); !got.Allowed {
		t.Error("only a create is judged")
	}
}
