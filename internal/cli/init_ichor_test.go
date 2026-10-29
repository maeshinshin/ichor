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
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/version"
)

func TestApplyIchorManifest_RefusesPlaceholder(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	err := applyIchorManifest(context.Background(), k8sClient, []byte(installmanifest.PlaceholderSentinel+"\n# nothing else"), &warn)
	if err == nil {
		t.Fatal("expected an error for the placeholder manifest, got nil")
	}
}

func TestApplyIchorManifest_AppliesEveryDocument(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("applyIchorManifest: %v", err)
	}

	var ns corev1.Namespace
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: ichorControllerNamespace}, &ns); err != nil {
		t.Fatalf("getting applied Namespace: %v", err)
	}

	var sa corev1.ServiceAccount
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: "ichor-controller"}, &sa); err != nil {
		t.Fatalf("getting applied ServiceAccount: %v", err)
	}
}

// TestApplyIchorManifest_AlreadyExistsIsNotAnError proves a re-run
// against a cluster that already has some of the manifest applied
// doesn't fail just because of that.
func TestApplyIchorManifest_AlreadyExistsIsNotAnError(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("second apply: %v", err)
	}
}

func newIchorInstallationFake(t *testing.T) client.Client {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func TestCreateIchorInstallation_RecordsTheBinarysVersion(t *testing.T) {
	old := version.Version
	version.Version = testReleasedVersion

	t.Cleanup(func() { version.Version = old })

	c := newIchorInstallationFake(t)

	var warn bytes.Buffer
	created, err := createIchorInstallation(context.Background(), c, &warn, 0)
	if err != nil || !created {
		t.Fatalf("createIchorInstallation = %v, %v", created, err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
		t.Fatalf("the singleton was not created: %v", err)
	}

	if got.Spec.Version != testReleasedVersion {
		t.Errorf("spec = %+v, want version v0.3.1", got.Spec)
	}
}

func TestCreateIchorInstallation_SkipsADevBuild(t *testing.T) {
	old := version.Version
	version.Version = "dev"

	t.Cleanup(func() { version.Version = old })

	c := newIchorInstallationFake(t)

	var warn bytes.Buffer
	if created, err := createIchorInstallation(context.Background(), c, &warn, 0); err != nil || created {
		t.Fatalf("createIchorInstallation = %v, %v", created, err)
	}

	var list ichorv1alpha1.IchorInstallationList
	if err := c.List(context.Background(), &list); err != nil || len(list.Items) != 0 {
		t.Errorf("a dev build created %d IchorInstallations (err %v)", len(list.Items), err)
	}

	if !strings.Contains(warn.String(), "not a released version") {
		t.Errorf("no notice was printed: %q", warn.String())
	}
}

// shrinkInitRetry makes init's apply retries fast.
func shrinkInitRetry(t *testing.T) {
	t.Helper()

	w, d := initRetryWindow, followRetryDelay
	initRetryWindow, followRetryDelay = time.Second, time.Millisecond

	t.Cleanup(func() { initRetryWindow, followRetryDelay = w, d })
}

// failingCreates fails the first n Creates with err, then lets the fake answer.
func failingCreates(t *testing.T, n int, err error, calls *int) client.Client {
	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			*calls++
			if *calls <= n {
				return err
			}

			return c.Create(ctx, obj, opts...)
		},
	}).Build()
}

func TestApplyIchorManifest_RetriesEtcdTimeout(t *testing.T) {
	shrinkInitRetry(t)

	calls := 0
	etcd := apierrors.NewInternalError(errors.New("etcdserver: request timed out"))

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), failingCreates(t, 2, etcd, &calls), []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("applyIchorManifest: %v", err)
	}

	if got := strings.Count(warn.String(), "init: retrying"); got != 2 {
		t.Fatalf("want 2 retrying lines, got %d:\n%s", got, warn.String())
	}
}

func TestApplyIchorManifest_ForbiddenFailsAtOnce(t *testing.T) {
	shrinkInitRetry(t)

	calls := 0
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "x", errors.New("no"))

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), failingCreates(t, 100, forbidden, &calls), []byte(testIchorManifest), &warn); err == nil {
		t.Fatal("want the Forbidden error, got nil")
	}

	if calls != 1 || strings.Contains(warn.String(), "retrying") {
		t.Fatalf("Forbidden must not be retried: %d calls, output %q", calls, warn.String())
	}
}

func TestIsTransient_EtcdTimeout(t *testing.T) {
	for _, msg := range []string{"etcdserver: request timed out", "etcdserver: leader changed"} {
		if !isTransient(apierrors.NewInternalError(errors.New(msg))) {
			t.Errorf("%q should be transient", msg)
		}
	}

	if isTransient(apierrors.NewInternalError(errors.New("boom"))) {
		t.Error("an unrelated InternalError must not be transient")
	}
}

// adopt's install step creates the IchorInstallation through the same bounded webhook wait as init.
func TestCreateIchorInstallation_RetriesWhileTheWebhookIsDown(t *testing.T) {
	old := version.Version
	version.Version = testReleasedVersion

	t.Cleanup(func() { version.Version = old })
	shrinkInitRetry(t)

	var calls int

	webhookDown := apierrors.NewInternalError(errors.New(`failed calling webhook "vichorinstallation.kb.io": Post "https://svc:443/validate": dial tcp 10.96.0.1:443: operation not permitted`))
	c := failingCreates(t, 2, webhookDown, &calls)

	created, err := createIchorInstallation(context.Background(), c, io.Discard, 0)
	if err != nil || !created {
		t.Fatalf("createIchorInstallation = %v, %v", created, err)
	}

	if calls != 3 {
		t.Errorf("Create called %d times, want 3 (two webhook failures, then success)", calls)
	}
}

// adopt passes its --wait-timeout: a webhook that never comes up ends the wait at that bound,
// not at init's 30 minutes.
func TestCreateIchorInstallation_HonoursTheCallersWait(t *testing.T) {
	old := version.Version
	version.Version = testReleasedVersion

	t.Cleanup(func() { version.Version = old })
	shrinkInitRetry(t)

	var calls int

	webhookDown := apierrors.NewInternalError(errors.New(`failed calling webhook "vichorinstallation.kb.io": connection refused`))
	c := failingCreates(t, 1<<30, webhookDown, &calls)

	start := time.Now()
	_, err := createIchorInstallation(context.Background(), c, io.Discard, 100*time.Millisecond)

	if err == nil {
		t.Fatal("createIchorInstallation succeeded against a webhook that never answers")
	}

	if time.Since(start) > 30*time.Second {
		t.Errorf("the wait ran %v, want about the 100ms the caller gave", time.Since(start))
	}
}
