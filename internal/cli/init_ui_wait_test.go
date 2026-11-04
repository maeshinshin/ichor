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
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func uiDeployment(available int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		Namespace: ichorControllerNamespace, Name: "ichor-ichor-ui", Generation: 1,
		Labels: map[string]string{uiComponentLabel: uiComponentValue},
		Spec:   appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: available},
	}
}

func realUIWait(t *testing.T) {
	t.Helper()

	old := waitForUI
	waitForUI = waitForUIRollout

	t.Cleanup(func() { waitForUI = old })
}

func TestWaitForUIReady_ReadyDeploymentEndsTheWait(t *testing.T) {
	realUIWait(t)

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiDeployment(1)).Build()

	var stderr bytes.Buffer

	deps := initDeps{Stderr: &stderr, WebhookWait: time.Second, BootstrapInterval: time.Millisecond}
	if err := waitForUIReady(context.Background(), c, deps); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stderr.String(), "ichor-ui is ready") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

// A Deployment that never becomes available is a warning and a nil error: init has succeeded.
func TestWaitForUIReady_TimeoutWarnsAndReturnsNil(t *testing.T) {
	realUIWait(t)

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiDeployment(0)).Build()

	var stderr bytes.Buffer

	deps := initDeps{Stderr: &stderr, WebhookWait: 50 * time.Millisecond, BootstrapInterval: 5 * time.Millisecond}
	if err := waitForUIReady(context.Background(), c, deps); err != nil {
		t.Fatalf("a timeout must not fail init: %v", err)
	}

	if !strings.Contains(stderr.String(), "warning: ichor-ui was not ready") {
		t.Errorf("stderr lacks the warning:\n%s", stderr.String())
	}
}

// runInit waits for ichor-ui, bounded by --wait-timeout, before it returns.
func TestRunInit_WaitsForTheUIBeforeReturning(t *testing.T) {
	withTempHome(t)

	var gotTimeout time.Duration

	old := waitForUI
	waitForUI = func(_ context.Context, _ client.Reader, timeout, _ time.Duration, _ io.Writer) error {
		gotTimeout = timeout

		return nil
	}

	t.Cleanup(func() { waitForUI = old })

	deps, _, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())
	deps.WebhookWait = 7 * time.Minute

	if err := runInit(context.Background(), validInitOptions(), deps); err != nil {
		t.Fatal(err)
	}

	if gotTimeout != 7*time.Minute {
		t.Errorf("ichor-ui wait timeout = %v, want --wait-timeout (7m)", gotTimeout)
	}
}
