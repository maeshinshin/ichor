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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestCNIOperationIsRefusedForAnUnmanagedCluster checks: a cni-kind TalosOperation
// (chart upgrade or kube-proxy switch) on an unmanaged cluster is refused, whoever asks; on a
// cilium cluster it is admitted.
func TestCNIOperationIsRefusedForAnUnmanagedCluster(t *testing.T) {
	t.Parallel()

	scheme := schemeOf(t)

	cluster := func(name, preset string) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Name, c.Namespace = name, testNamespace
		c.Spec.CNI.Preset = preset

		return c
	}

	h := &OverrideHandler{
		Client:      fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster("adopted", "unmanaged"), cluster("own", "cilium")).Build(),
		ExemptUsers: DefaultExemptUsers("ichor-system", "ichor-controller"),
	}

	request := func(clusterName, user, action string) admission.Request {
		op := ichorv1alpha1.TalosOperation{
			Name: "op", Namespace: testNamespace,
			Spec: ichorv1alpha1.TalosOperationSpec{
				Kind:       ichorv1alpha1.TalosOperationKindCNI,
				ClusterRef: corev1.LocalObjectReference{Name: clusterName},
				CNI:        &ichorv1alpha1.TalosOperationCNISpec{Action: action},
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

	for _, action := range []string{ichorv1alpha1.TalosOperationCNIActionUpgrade, ""} {
		for _, user := range []string{bgUnitPlain, "system:serviceaccount:ichor-system:ichor-controller"} {
			r := h.Handle(context.Background(), request("adopted", user, action))
			if r.Allowed || r.Result.Message != ichorv1alpha1.CNINotManagedMessage {
				t.Errorf("action %q by %s on an unmanaged cluster: allowed=%v message=%q", action, user, r.Allowed, r.Result.Message)
			}
		}
	}

	if r := h.Handle(context.Background(), request("own", bgUnitPlain, ichorv1alpha1.TalosOperationCNIActionUpgrade)); r.Result != nil &&
		r.Result.Message == ichorv1alpha1.CNINotManagedMessage {
		t.Errorf("a cilium cluster's cni operation was refused as unmanaged")
	}
}
