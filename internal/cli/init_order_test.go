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
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// A real API server refuses a Secret in a namespace that does not exist, and the namespace
// comes from Ichor's own install manifest, so the bundle Secret must be created after it.
func TestRunInit_BundleSecretIsCreatedAfterTheNamespaceExists(t *testing.T) {
	withTempHome(t)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if s, ok := obj.(*corev1.Secret); ok {
				var ns corev1.Namespace
				if err := c.Get(ctx, types.NamespacedName{Name: s.Namespace}, &ns); apierrors.IsNotFound(err) {
					return fmt.Errorf("namespaces %q not found", s.Namespace)
				}
			}

			return c.Create(ctx, obj, opts...)
		},
	}).Build()

	deps, _, _ := newFakeInitDeps(t, k8sClient)

	opts := validInitOptions()
	opts.Namespace = ichorControllerNamespace

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}
}
