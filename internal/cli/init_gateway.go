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
	"fmt"
	"io"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/cni/gatewayapi"
)

// gatewayAPIFieldOwner is the server-side-apply manager of the Gateway API CRDs. It is not the installer's, so an
// `ichor upgrade` never owns them and never removes what a release drops.
const gatewayAPIFieldOwner = "ichor-init-gateway-api"

// gatewayAPIBundleVersionAnnotation names the Gateway API release a CRD comes from; the embedded CRDs carry it.
const gatewayAPIBundleVersionAnnotation = "gateway.networking.k8s.io/bundle-version"

// applyGatewayAPICRDs applies the Gateway API CRDs Cilium's gatewayAPI needs, through the API server once it
// answers, as `ichor init --gateway-api` does. They are in no Talos configuration, so no CNI hand-over can drop
// them, and this only applies: it never deletes.
//
// A CRD another tool installed is not Ichor's to write. One at the embedded release is left as it is; one at
// another release refuses the whole run before anything is written, because Cilium needs v1.6.1 and Ichor
// does not replace a CRD it did not create. A CRD Ichor installed earlier is updated in place.
func applyGatewayAPICRDs(ctx context.Context, k8sClient client.Client, warn io.Writer) error {
	objs, err := installer.Decode(gatewayapi.CRDs())
	if err != nil {
		return fmt.Errorf("init: decoding the Gateway API CRDs: %w", err)
	}

	var writes []*unstructured.Unstructured

	for _, obj := range objs {
		have, err := gatewayAPICRDOnCluster(ctx, k8sClient, warn, obj.GetName())
		if err != nil {
			return err
		}

		switch {
		case have == nil || applyOwnedByGatewayAPIManager(have):
			writes = append(writes, obj)
		case have.GetAnnotations()[gatewayAPIBundleVersionAnnotation] == obj.GetAnnotations()[gatewayAPIBundleVersionAnnotation]:
			_, _ = fmt.Fprintf(warn, "init: %s is already installed by another tool at the same release; left as it is\n", obj.GetName())
		default:
			return fmt.Errorf("init: %s is installed by another tool at Gateway API %s; Ichor applies %s and does not "+
				"replace a CRD it did not install: remove or upgrade it yourself, then run init again",
				obj.GetName(), have.GetAnnotations()[gatewayAPIBundleVersionAnnotation],
				obj.GetAnnotations()[gatewayAPIBundleVersionAnnotation])
		}
	}

	for _, obj := range writes {
		err := retryInit(ctx, warn, func() error {
			return k8sClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), client.FieldOwner(gatewayAPIFieldOwner))
		})
		if err != nil {
			return fmt.Errorf("init: applying Gateway API CRD %q: %w", obj.GetName(), err)
		}
	}

	_, _ = fmt.Fprintf(warn, "init: applied the Gateway API CRDs (%d)\n", len(writes))

	return nil
}

// gatewayAPICRDOnCluster is the CRD named name as the cluster has it, or nil when it is absent.
func gatewayAPICRDOnCluster(ctx context.Context, k8sClient client.Client, warn io.Writer, name string) (*unstructured.Unstructured, error) {
	crd := &unstructured.Unstructured{}
	crd.SetAPIVersion("apiextensions.k8s.io/v1")
	crd.SetKind("CustomResourceDefinition")

	err := retryInit(ctx, warn, func() error {
		return k8sClient.Get(ctx, types.NamespacedName{Name: name}, crd)
	})

	switch {
	case err == nil:
		return crd, nil
	case apierrors.IsNotFound(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("init: reading Gateway API CRD %q: %w", name, err)
	}
}

// applyOwnedByGatewayAPIManager reports whether Ichor's own field manager applied the CRD before.
func applyOwnedByGatewayAPIManager(crd *unstructured.Unstructured) bool {
	return slices.ContainsFunc(crd.GetManagedFields(), func(e metav1.ManagedFieldsEntry) bool {
		return e.Manager == gatewayAPIFieldOwner && e.Operation == metav1.ManagedFieldsOperationApply
	})
}
