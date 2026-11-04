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
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestMain: the tests that drive init and adopt against a fake cluster have no controller
// Deployment to wait for, so the readiness wait is off for them. The tests of the wait itself
// call waitForWebhookServer directly or put the real one back with realWebhookWait.
func TestMain(m *testing.M) {
	waitForController = func(context.Context, client.Reader, time.Duration, time.Duration, io.Writer) error { return nil }

	os.Exit(m.Run())
}

func realWebhookWait(t *testing.T) {
	t.Helper()

	old := waitForController
	waitForController = waitForWebhookServer

	t.Cleanup(func() { waitForController = old })
}

func readyControllerDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		Namespace: ichorControllerNamespace, Name: controllerDeploymentName, Generation: 2,
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: available,
		},
	}
}

func webhookSlice(ready *bool) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		Namespace: ichorControllerNamespace, Name: webhookServiceName + "-abcde",
		Labels:      map[string]string{discoveryv1.LabelServiceName: webhookServiceName},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.7"}, Conditions: discoveryv1.EndpointConditions{Ready: ready}}},
	}
}

func TestWaitForWebhookServer_ReadyAtOnce(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(readyControllerDeployment(1), webhookSlice(nil)).Build() // a nil Ready means ready

	if err := waitForWebhookServer(context.Background(), c, time.Second, time.Millisecond, &bytes.Buffer{}); err != nil {
		t.Fatalf("waitForWebhookServer: %v", err)
	}
}

// The Deployment has no available replica at first and is available a few polls later: the wait
// ends then, not before and not at the timeout.
func TestWaitForWebhookServer_WaitsUntilTheDeploymentIsAvailable(t *testing.T) {
	gets := 0

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(readyControllerDeployment(0), webhookSlice(ptr.To(true))).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if d, ok := obj.(*appsv1.Deployment); ok {
					gets++

					if err := cl.Get(ctx, key, d, opts...); err != nil {
						return err
					}

					if gets >= 3 {
						d.Status.AvailableReplicas = 1
					}

					return nil
				}

				return cl.Get(ctx, key, obj, opts...)
			},
		}).Build()

	var out bytes.Buffer

	if err := waitForWebhookServer(context.Background(), c, 30*time.Second, time.Millisecond, &out); err != nil {
		t.Fatalf("waitForWebhookServer: %v", err)
	}

	if gets != 3 {
		t.Errorf("the Deployment was read %d times, want 3", gets)
	}
}

func TestWaitForWebhookServer_TimesOutWithAnActionableMessage(t *testing.T) {
	for name, objs := range map[string][]client.Object{
		"no Deployment":             {webhookSlice(ptr.To(true))},
		"no available replica":      {readyControllerDeployment(0), webhookSlice(ptr.To(true))},
		"no endpoint":               {readyControllerDeployment(1)},
		"the endpoint is not ready": {readyControllerDeployment(1), webhookSlice(ptr.To(false))},
	} {
		t.Run(name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()

			start := time.Now()
			err := waitForWebhookServer(context.Background(), c, 50*time.Millisecond, time.Millisecond, &bytes.Buffer{})

			if err == nil {
				t.Fatal("waitForWebhookServer succeeded on a controller that is not ready")
			}

			for _, want := range []string{"ichor-controller-manager", ichorControllerNamespace, "not Ready after 50ms", "kubectl -n ichor-system get pods"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}

			if time.Since(start) > 10*time.Second {
				t.Errorf("the wait ran %v, want about 50ms", time.Since(start))
			}
		})
	}
}

// Nothing is written before the controller is ready: the first Create of a CR is the one that
// used to find out.
func TestCreateWhenWebhooksUp_WaitsForTheControllerBeforeWriting(t *testing.T) {
	realWebhookWait(t)

	creates := 0

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			creates++

			return cl.Create(ctx, obj, opts...)
		},
	}).Build()

	cm := &ichorv1alpha1.IchorInstallation{Name: "x"}

	if err := createWhenWebhooksUp(context.Background(), c, cm, 50*time.Millisecond, time.Millisecond, &bytes.Buffer{}); err == nil {
		t.Fatal("createWhenWebhooksUp succeeded with no controller")
	}

	if creates != 0 {
		t.Fatalf("Create was called %d times before the controller was ready", creates)
	}

	for _, o := range []client.Object{readyControllerDeployment(1), webhookSlice(ptr.To(true))} {
		if err := c.Create(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}

	creates = 0

	if err := createWhenWebhooksUp(context.Background(), c, cm, time.Second, time.Millisecond, &bytes.Buffer{}); err != nil {
		t.Fatalf("createWhenWebhooksUp: %v", err)
	}

	if creates != 1 {
		t.Errorf("Create called %d times, want 1", creates)
	}
}
