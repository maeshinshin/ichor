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
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var errWebhookDown = errors.New(`failed calling webhook "vtaloscluster-v1alpha1.kb.io": no endpoints available`)

// The webhooks Ichor's manifest registers have failurePolicy Fail and are served by the
// controller that manifest has only just started, so the first CR writes see "failed calling
// webhook" until it is up. init waits instead of failing.
func TestRunInit_WaitsForTheControllersWebhooks(t *testing.T) {
	withTempHome(t)
	shrinkInitRetry(t)

	failures := 3

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosCluster); ok && failures > 0 {
				failures--

				return apierrors.NewInternalError(errWebhookDown)
			}

			return c.Create(ctx, obj, opts...)
		},
	}).Build()

	deps, _, _ := newFakeInitDeps(t, k8sClient)
	deps.BootstrapInterval = time.Millisecond

	if err := runInit(context.Background(), validInitOptions(), deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	if failures != 0 {
		t.Fatalf("the TalosCluster create was not retried (%d failures left)", failures)
	}
}
