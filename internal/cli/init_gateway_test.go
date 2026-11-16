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

package cli

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/cni/gatewayapi"
)

// gatewayCRDRecorder is a client whose only calls are Get and Apply; any other method is nil and panics,
// so a test fails if the CRD path ever patches or deletes anything. crds holds the CRDs already on the
// cluster by name; a name not in it is NotFound.
type gatewayCRDRecorder struct {
	client.Client

	crds map[string]*unstructured.Unstructured

	names  []runtime.ApplyConfiguration
	owners []string
	forced []bool
}

func (r *gatewayCRDRecorder) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	crd, ok := r.crds[key.Name]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, key.Name)
	}

	obj.(*unstructured.Unstructured).Object = crd.DeepCopy().Object

	return nil
}

func (r *gatewayCRDRecorder) Apply(_ context.Context, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
	o := &client.ApplyOptions{}
	for _, opt := range opts {
		opt.ApplyToApply(o)
	}

	r.owners = append(r.owners, o.FieldManager)
	r.forced = append(r.forced, o.Force != nil && *o.Force)

	r.names = append(r.names, obj)

	return nil
}

// foreignCRD is a CRD another tool installed: its bundle-version annotation and a managedFields entry for
// that tool's own manager, not Ichor's.
func foreignCRD(name, bundleVersion string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetAPIVersion("apiextensions.k8s.io/v1")
	u.SetKind("CustomResourceDefinition")
	u.SetName(name)
	u.SetAnnotations(map[string]string{gatewayAPIBundleVersionAnnotation: bundleVersion})
	u.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kubectl-client-side-apply", Operation: metav1.ManagedFieldsOperationUpdate}})

	return u
}

// TestApplyGatewayAPICRDsOnlyApplies applies the seven CRDs under their own field manager, without force,
// into an empty cluster, and never deletes, patches or reads anything but the CRDs by name.
func TestApplyGatewayAPICRDsOnlyApplies(t *testing.T) {
	r := &gatewayCRDRecorder{}

	if err := applyGatewayAPICRDs(context.Background(), r, io.Discard); err != nil {
		t.Fatalf("applyGatewayAPICRDs: %v", err)
	}

	want := []string{"backendtlspolicies", "gatewayclasses", "gateways", "grpcroutes", "httproutes", "referencegrants", "tlsroutes"}

	if len(r.names) != len(want) {
		t.Fatalf("applied %d CRDs, want %d", len(r.names), len(want))
	}

	objs, err := installer.Decode(gatewayapi.CRDs())
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(objs))
	for _, o := range objs {
		names = append(names, o.GetName())
	}

	for _, w := range want {
		if !slices.Contains(names, w+".gateway.networking.k8s.io") {
			t.Errorf("the embedded CRDs lack %s.gateway.networking.k8s.io", w)
		}
	}

	for _, o := range r.owners {
		if o != gatewayAPIFieldOwner {
			t.Fatalf("applied under field manager %q, want %q", o, gatewayAPIFieldOwner)
		}
	}
}

// TestApplyGatewayAPICRDsRefusesAForeignCRDAtAnotherVersion: a CRD Ichor did not install, at a Gateway
// API release other than the one embedded, makes the run refuse before it writes anything.
func TestApplyGatewayAPICRDsRefusesAForeignCRDAtAnotherVersion(t *testing.T) {
	r := &gatewayCRDRecorder{crds: map[string]*unstructured.Unstructured{
		"gateways.gateway.networking.k8s.io": foreignCRD("gateways.gateway.networking.k8s.io", "v1.5.0"),
	}}

	err := applyGatewayAPICRDs(context.Background(), r, io.Discard)
	if err == nil {
		t.Fatal("applyGatewayAPICRDs returned nil, want a refusal naming the foreign CRD at v1.5.0")
	}

	if !strings.Contains(err.Error(), "gateways.gateway.networking.k8s.io") || !strings.Contains(err.Error(), "v1.5.0") {
		t.Errorf("refusal %q does not name the CRD and its version", err)
	}

	if len(r.names) != 0 {
		t.Fatalf("applied %d CRDs before refusing, want none", len(r.names))
	}
}

// TestApplyGatewayAPICRDsLeavesAForeignCRDAtTheSameVersion: a CRD Ichor did not install, already at the
// embedded release, is left as it is; the other six are applied as usual.
func TestApplyGatewayAPICRDsLeavesAForeignCRDAtTheSameVersion(t *testing.T) {
	objs, err := installer.Decode(gatewayapi.CRDs())
	if err != nil {
		t.Fatal(err)
	}

	gateways := "gateways.gateway.networking.k8s.io"
	i := slices.IndexFunc(objs, func(o *unstructured.Unstructured) bool { return o.GetName() == gateways })
	if i < 0 {
		t.Fatalf("the embedded CRDs lack %s", gateways)
	}

	r := &gatewayCRDRecorder{crds: map[string]*unstructured.Unstructured{
		gateways: foreignCRD(gateways, objs[i].GetAnnotations()[gatewayAPIBundleVersionAnnotation]),
	}}

	if err := applyGatewayAPICRDs(context.Background(), r, io.Discard); err != nil {
		t.Fatalf("applyGatewayAPICRDs: %v", err)
	}

	if len(r.names) != 6 {
		t.Fatalf("applied %d CRDs, want 6 (the foreign gateways CRD is left alone)", len(r.names))
	}

	for _, n := range r.names {
		if named, ok := n.(interface{ GetName() *string }); ok && *named.GetName() == gateways {
			t.Fatalf("applied %s, which Ichor did not install", gateways)
		}
	}
}

// TestApplyGatewayAPICRDsDoesNotForce: the apply carries no force, so a field another manager owns on an
// existing CRD is never taken over.
func TestApplyGatewayAPICRDsDoesNotForce(t *testing.T) {
	r := &gatewayCRDRecorder{}

	if err := applyGatewayAPICRDs(context.Background(), r, io.Discard); err != nil {
		t.Fatalf("applyGatewayAPICRDs: %v", err)
	}

	for _, f := range r.forced {
		if f {
			t.Fatal("applied with force: a CRD another tool owns would be taken over")
		}
	}
}
