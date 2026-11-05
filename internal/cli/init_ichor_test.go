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
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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
	setVersion(t, testReleasedVersion)

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
	setVersion(t, "dev")

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
// setVersion makes the binary report v until the test ends.
func setVersion(t *testing.T, v string) {
	t.Helper()

	old := version.Version
	version.Version = v

	t.Cleanup(func() { version.Version = old })
}

func shrinkInitRetry(t *testing.T) {
	t.Helper()

	w, d := initRetryWindow, followRetryDelay
	initRetryWindow, followRetryDelay = time.Second, time.Millisecond

	t.Cleanup(func() { initRetryWindow, followRetryDelay = w, d })
}

// beforeWrites runs before on every Create and Apply, fails the write with its error, else lets the fake answer.
func beforeWrites(before func() error) interceptor.Funcs {
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if err := before(); err != nil {
				return err
			}

			return c.Create(ctx, obj, opts...)
		},
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if err := before(); err != nil {
				return err
			}

			return c.Apply(ctx, obj, opts...)
		},
	}
}

// failingCreates fails the first n writes with err, then lets the fake answer.
func failingCreates(t *testing.T, n int, err error, calls *int) client.Client {
	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(beforeWrites(func() error {
		*calls++
		if *calls <= n {
			return err
		}

		return nil
	})).Build()
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

// A webhook that cannot be called (its pod restarting with the control plane) comes back as a 500
// InternalError whatever the cause (refused, timed out, no endpoints); a webhook that answered "no"
// is a Forbidden or Invalid refusal and must fail at once, even if its reason quotes such text.
func TestIsTransient_WebhookCallFailures(t *testing.T) {
	gr := schema.GroupResource{Group: "ichor.maesh.dev", Resource: "talosoperations"}
	call := func(cause string) error {
		return apierrors.NewInternalError(errors.New("failed calling webhook \"voverrideop-v1alpha1.kb.io\": " +
			"failed to call webhook: Post \"https://ichor-webhook-service.ichor-system.svc:443/validate\": " + cause))
	}

	for name, err := range map[string]error{
		"refused":        call("dial tcp 10.249.23.203:443: connect: connection refused"),
		"deadline":       call("context deadline exceeded"),
		"client timeout": call("net/http: request canceled (Client.Timeout exceeded while awaiting headers)"),
		"no endpoints":   call("no endpoints available for service \"ichor-webhook-service\""),
		"unavailable":    apierrors.NewServiceUnavailable("failed calling webhook: no endpoints"),
	} {
		if !isTransient(err) {
			t.Errorf("%s: %v should be transient", name, err)
		}
	}

	for name, err := range map[string]error{
		"denied": apierrors.NewForbidden(gr, "x", errors.New("admission webhook \"voverrideop-v1alpha1.kb.io\" denied the request: tokenSettle is too short")),
		"denied quoting a call failure": apierrors.NewForbidden(gr, "x",
			errors.New("denied: earlier failed calling webhook text: context deadline exceeded")),
		"invalid": apierrors.NewInvalid(schema.GroupKind{Group: gr.Group, Kind: "TalosOperation"}, "x", nil),
	} {
		if isTransient(err) {
			t.Errorf("%s: %v must fail fast", name, err)
		}
	}
}

// While the kube-apiserver of the endpoint comes back, the client's REST mapper cannot discover the
// ichor group yet: that is discovery failing, not an answer.
func TestIsTransient_DiscoveryFailures(t *testing.T) {
	gv := ichorv1alpha1.GroupVersion
	mapperFailed := apiutil.ErrResourceDiscoveryFailed{gv: apierrors.NewNotFound(schema.GroupResource{}, "")}
	groupFailed := &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{gv: errors.New("503")}}

	for name, err := range map[string]error{
		"no match":            &meta.NoResourceMatchError{PartialResource: gv.WithResource("")},
		"wrapped no match":    fmt.Errorf("patching: %w", &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: gv.Group, Kind: "Thing"}}),
		"rest mapper":         &mapperFailed,
		"wrapped rest mapper": fmt.Errorf("acknowledging: %w", &mapperFailed),
		"group discovery":     groupFailed,
		"wrapped group":       fmt.Errorf("listing: %w", groupFailed),
	} {
		if !isTransient(err) {
			t.Errorf("%s: %v should be transient", name, err)
		}
	}

	gr := schema.GroupResource{Group: gv.Group, Resource: "things"}

	for name, err := range map[string]error{
		"forbidden": apierrors.NewForbidden(gr, "x", errors.New("no match for this user")),
		"not found": apierrors.NewNotFound(gr, "x"),
	} {
		if isTransient(err) {
			t.Errorf("%s: %v must fail fast", name, err)
		}
	}
}

// adopt's install step creates the IchorInstallation through the same bounded webhook wait as init.
func TestCreateIchorInstallation_RetriesWhileTheWebhookIsDown(t *testing.T) {
	setVersion(t, testReleasedVersion)
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

// adopt passes its --wait-timeout: a controller that never becomes Ready ends the wait at that
// bound, with nothing written.
func TestCreateIchorInstallation_HonoursTheCallersWait(t *testing.T) {
	setVersion(t, testReleasedVersion)
	realWebhookWait(t)

	var calls int

	c := failingCreates(t, 0, nil, &calls)

	start := time.Now()
	_, err := createIchorInstallation(context.Background(), c, io.Discard, 100*time.Millisecond)

	if err == nil || !strings.Contains(err.Error(), "ichor-controller-manager") {
		t.Fatalf("createIchorInstallation = %v, want the not-Ready message", err)
	}

	if calls != 0 {
		t.Errorf("Create called %d times before the controller was ready", calls)
	}

	if time.Since(start) > 30*time.Second {
		t.Errorf("the wait ran %v, want about the 100ms the caller gave", time.Since(start))
	}
}
