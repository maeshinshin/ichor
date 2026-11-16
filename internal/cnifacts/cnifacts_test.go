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

package cnifacts

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// crdReader answers the one CRD Context reads: with its bundle-version annotation, or with NotFound when crd is nil.
type crdReader struct{ crd *metav1.PartialObjectMetadata }

func (r crdReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	if r.crd == nil || key.Name != GatewayAPICRD {
		return apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, key.Name)
	}

	r.crd.DeepCopyInto(obj.(*metav1.PartialObjectMetadata))

	return nil
}

func (crdReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return nil
}

// TestContextReadsGatewayAPIRelease checks that the release the guard compares is the gateways CRD's
// bundle-version annotation, and that an absent CRD reads as absent.
func TestContextReadsGatewayAPIRelease(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	cluster := &ichorv1alpha1.TalosCluster{Name: "c", Namespace: "ns"}
	src := func(r client.Reader) Source {
		return Source{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), CRDs: r, Namespace: "ns"}
	}
	crd := func(release string) *metav1.PartialObjectMetadata {
		return &metav1.PartialObjectMetadata{
			Name: GatewayAPICRD, Annotations: map[string]string{GatewayAPIBundleVersionAnnotation: release},
		}
	}

	cases := []struct {
		name    string
		crd     *metav1.PartialObjectMetadata
		exists  bool
		release string
	}{
		{name: "annotated", crd: crd("v1.5.1"), exists: true, release: "v1.5.1"},
		{name: "unannotated", crd: &metav1.PartialObjectMetadata{Name: GatewayAPICRD}, exists: true},
		{name: "absent", exists: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vc := src(crdReader{crd: tc.crd}).Context(context.Background(), cluster, cni.KubeProxyReplaced)

			if vc.GatewayAPICRDs == nil || *vc.GatewayAPICRDs != tc.exists {
				t.Fatalf("GatewayAPICRDs = %v, want %v", vc.GatewayAPICRDs, tc.exists)
			}

			if vc.GatewayAPIRelease != tc.release {
				t.Fatalf("GatewayAPIRelease = %q, want %q", vc.GatewayAPIRelease, tc.release)
			}
		})
	}
}
