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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/version"
)

func TestApplyIchorManifest_RefusesPlaceholder(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	err := applyIchorManifest(context.Background(), k8sClient, []byte(installmanifest.PlaceholderSentinel+"\n# nothing else"), &warn, false, true)
	if err == nil {
		t.Fatal("expected an error for the placeholder manifest, got nil")
	}
}

func TestApplyIchorManifest_AppliesEveryDocument(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn, false, true); err != nil {
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

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn, false, true); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn, false, true); err != nil {
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
	created, err := createIchorInstallation(context.Background(), c, &warn, 0, installerPin{})
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
	if created, err := createIchorInstallation(context.Background(), c, &warn, 0, installerPin{}); err != nil || created {
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

	if err := applyIchorManifest(context.Background(), failingCreates(t, 2, etcd, &calls), []byte(testIchorManifest), &warn, false, true); err != nil {
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

	if err := applyIchorManifest(context.Background(), failingCreates(t, 100, forbidden, &calls), []byte(testIchorManifest), &warn, false, true); err == nil {
		t.Fatal("want the Forbidden error, got nil")
	}

	if calls != 1 || strings.Contains(warn.String(), "retrying") {
		t.Fatalf("Forbidden must not be retried: %d calls, output %q", calls, warn.String())
	}
}

// isTransient is true for the API briefly unreachable and false for an answer. A webhook that cannot be
// called (its pod restarting with the control plane) comes back as a 500 InternalError whatever the cause;
// a webhook that answered "no" is a Forbidden or Invalid refusal and fails at once, even if its reason
// quotes such text. While the endpoint kube-apiserver comes back, the REST mapper cannot discover the
// ichor group yet: that is discovery failing, not an answer.
func TestIsTransient(t *testing.T) {
	gv := ichorv1alpha1.GroupVersion
	gr := schema.GroupResource{Group: gv.Group, Resource: "talosoperations"}
	mapperFailed := apiutil.ErrResourceDiscoveryFailed{gv: apierrors.NewNotFound(schema.GroupResource{}, "")}
	groupFailed := &discovery.ErrGroupDiscoveryFailed{Groups: map[schema.GroupVersion]error{gv: errors.New("503")}}
	internal := func(msg string) error { return apierrors.NewInternalError(errors.New(msg)) }
	call := func(cause string) error {
		return internal("failed calling webhook \"voverrideop-v1alpha1.kb.io\": " +
			"failed to call webhook: Post \"https://ichor-webhook-service.ichor-system.svc:443/validate\": " + cause)
	}

	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"etcd timeout":                  {internal("etcdserver: request timed out"), true},
		"etcd leader changed":           {internal("etcdserver: leader changed"), true},
		"unrelated internal":            {internal("boom"), false},
		"webhook refused":               {call("dial tcp 10.249.23.203:443: connect: connection refused"), true},
		"webhook deadline":              {call("context deadline exceeded"), true},
		"webhook client timeout":        {call("net/http: request canceled (Client.Timeout exceeded while awaiting headers)"), true},
		"webhook no endpoints":          {call("no endpoints available for service \"ichor-webhook-service\""), true},
		"webhook unavailable":           {apierrors.NewServiceUnavailable("failed calling webhook: no endpoints"), true},
		"webhook denied":                {apierrors.NewForbidden(gr, "x", errors.New("admission webhook \"voverrideop-v1alpha1.kb.io\" denied the request: tokenSettle is too short")), false},
		"denied quoting a call failure": {apierrors.NewForbidden(gr, "x", errors.New("denied: earlier failed calling webhook text: context deadline exceeded")), false},
		"webhook invalid":               {apierrors.NewInvalid(schema.GroupKind{Group: gv.Group, Kind: "TalosOperation"}, "x", nil), false},
		"no match":                      {&meta.NoResourceMatchError{PartialResource: gv.WithResource("")}, true},
		"wrapped no match":              {fmt.Errorf("patching: %w", &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: gv.Group, Kind: "Thing"}}), true},
		"rest mapper":                   {&mapperFailed, true},
		"wrapped rest mapper":           {fmt.Errorf("acknowledging: %w", &mapperFailed), true},
		"group discovery":               {groupFailed, true},
		"wrapped group":                 {fmt.Errorf("listing: %w", groupFailed), true},
		"forbidden":                     {apierrors.NewForbidden(gr, "x", errors.New("no match for this user")), false},
		"not found":                     {apierrors.NewNotFound(gr, "x"), false},
	} {
		if got := isTransient(tc.err); got != tc.want {
			t.Errorf("%s: isTransient(%v) = %t, want %t", name, tc.err, got, tc.want)
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

	created, err := createIchorInstallation(context.Background(), c, io.Discard, 0, installerPin{})
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
	_, err := createIchorInstallation(context.Background(), c, io.Discard, 100*time.Millisecond, installerPin{})

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

// testCiliumSecretsManifest is testIchorManifest plus the pair Ichor installs into cilium-secrets, the
// Cilium chart's own namespace, and a Role in a namespace that never exists.
const testCiliumSecretsManifest = testIchorManifest + `---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ichor-cni-secrets-role
  namespace: cilium-secrets
rules:
- apiGroups: [""]
  resources: [secrets]
  verbs: [get]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ichor-cni-secrets-rolebinding
  namespace: cilium-secrets
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ichor-cni-secrets-role
subjects:
- kind: ServiceAccount
  name: ichor-cni
  namespace: kube-system
`

// namespaceGatedClient is a fake that answers a namespaced apply into a namespace it does not hold with
// the API server's "namespaces X not found", as a real cluster does before Cilium's bootstrap has run.
func namespaceGatedClient(t *testing.T) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			raw, err := json.Marshal(obj)
			if err != nil {
				return err
			}

			u := &unstructured.Unstructured{}
			if err := u.UnmarshalJSON(raw); err != nil {
				return err
			}

			if ns := u.GetNamespace(); ns != "" {
				if err := c.Get(ctx, types.NamespacedName{Name: ns}, &corev1.Namespace{}); err != nil {
					return err
				}
			}

			return c.Apply(ctx, obj, opts...)
		},
	}).Build()
}

// TestApplyIchorManifest_CreatesCiliumSecretsForCilium: init lost a race against Cilium's bootstrap twice on
// hardware (the namespace comes from Cilium's inline manifest, minutes after the API server). On a Cilium
// cluster init creates the namespace itself, so the Role and RoleBinding exist before Cilium needs them.
func TestApplyIchorManifest_CreatesCiliumSecretsForCilium(t *testing.T) {
	c := namespaceGatedClient(t)

	if err := applyIchorManifest(context.Background(), c, []byte(testCiliumSecretsManifest), &bytes.Buffer{}, true, true); err != nil {
		t.Fatalf("applyIchorManifest: %v", err)
	}

	if err := c.Get(context.Background(), types.NamespacedName{Name: installer.CiliumSecretsNamespace}, &corev1.Namespace{}); err != nil {
		t.Fatalf("the cilium-secrets Namespace was not created: %v", err)
	}

	for _, key := range []types.NamespacedName{{Namespace: "cilium-secrets", Name: "ichor-cni-secrets-role"}} {
		if err := c.Get(context.Background(), key, &rbacv1.Role{}); err != nil {
			t.Fatalf("Role %s: %v", key, err)
		}
	}

	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "cilium-secrets", Name: "ichor-cni-secrets-rolebinding"}, &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("RoleBinding: %v", err)
	}
}

// TestApplyIchorManifest_SkipsCiliumSecretsWithoutCilium: another CNI never creates the namespace, so the
// pair is skipped (as the installer does) and init does not create a namespace nothing would use.
func TestApplyIchorManifest_SkipsCiliumSecretsWithoutCilium(t *testing.T) {
	c := namespaceGatedClient(t)

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), c, []byte(testCiliumSecretsManifest), &warn, false, true); err != nil {
		t.Fatalf("applyIchorManifest: %v", err)
	}

	if err := c.Get(context.Background(), types.NamespacedName{Name: installer.CiliumSecretsNamespace}, &corev1.Namespace{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the cilium-secrets Namespace: got %v, want not found", err)
	}

	if !strings.Contains(warn.String(), "skipping Role ichor-cni-secrets-role") {
		t.Fatalf("no skip line:\n%s", warn.String())
	}
}

// TestApplyIchorManifest_MissingNamespaceElsewhereStillFails: only cilium-secrets is skipped.
func TestApplyIchorManifest_MissingNamespaceElsewhereStillFails(t *testing.T) {
	manifest := strings.ReplaceAll(testCiliumSecretsManifest, "namespace: cilium-secrets", "namespace: elsewhere")

	err := applyIchorManifest(context.Background(), namespaceGatedClient(t), []byte(manifest), &bytes.Buffer{}, true, true)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v, want the not found error", err)
	}
}
