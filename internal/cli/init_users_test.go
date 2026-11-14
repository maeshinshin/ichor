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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installer"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const testUIURL = "https://ichor.example.com"

const (
	testPrefixedUIDeployment = "ichor-ichor-ui"
	testControllerDeployment = "ichor-controller-manager"
	testUIClusterIP          = "10.96.0.42"
)

// uiObjects is what the embedded kustomize manifest installs: every name carries
// the namePrefix, so only the labels identify the objects.
func uiObjects() []client.Object {
	return []client.Object{
		&appsv1.Deployment{
			Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment,
			Labels: map[string]string{uiComponentLabel: uiComponentValue},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: uiContainer, Args: []string{"--addr=:8080"}}},
			}}},
		},
		&appsv1.Deployment{
			Namespace: ichorControllerNamespace, Name: testControllerDeployment,
			Labels: map[string]string{controllerLabel: controllerLabelValue},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: controllerContainer, Args: []string{"--leader-elect"}}},
			}}},
		},
		&corev1.Service{
			Namespace: ichorControllerNamespace, Name: testUIServiceName,
			Labels: map[string]string{uiComponentLabel: uiComponentValue},
			Spec:   corev1.ServiceSpec{ClusterIP: testUIClusterIP, Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
		},
	}
}

// generatedAdminPassword pulls the password out of init's stderr, where it is
// printed exactly once.
func generatedAdminPassword(t *testing.T, stderr string) string {
	t.Helper()

	const marker = "must change it at the first sign-in): "

	if strings.Count(stderr, marker) != 1 {
		t.Fatalf("the password should be printed exactly once:\n%s", stderr)
	}

	_, rest, _ := strings.Cut(stderr, marker)

	return strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0])
}

func TestRunInit_UIURLTurnsUsersOnAndCreatesTheFirstAdmin(t *testing.T) {
	home := withTempHome(t)

	var updateOrder []string

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if d, ok := obj.(*appsv1.Deployment); ok {
				// The switch comes after the admin exists (create the first admin before it).
				var u ichorv1alpha1.IchorUser
				if err := c.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testAdminName}, &u); err != nil {
					t.Errorf("the Deployment was switched before the admin existed: %v", err)
				}

				updateOrder = append(updateOrder, d.Name)
			}

			return c.Update(ctx, obj, opts...)
		},
	}).Build()

	deps, _, _ := newFakeInitDeps(t, k)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	opts := validInitOptions()
	opts.UIURL, opts.AdminUser = testUIURL, testAdminName
	opts.AllowUnauthenticated = false

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	ctx := context.Background()

	var u ichorv1alpha1.IchorUser
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testAdminName}, &u); err != nil || !slices.Equal(u.Spec.Roles, []string{ichorauth.RoleAdmin}) {
		t.Fatalf("admin user: %v %+v", err, u.Spec)
	}

	var sec corev1.Secret
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName}, &sec); err != nil {
		t.Fatal(err)
	}

	cred, err := ichorauth.ParseCredential(sec.Data[testAdminName])
	if err != nil || !cred.MustChange {
		t.Fatalf("credential: %v %+v", err, cred)
	}

	pw := generatedAdminPassword(t, stderr.String())
	if ok, _, err := cred.Verify(pw); err != nil || !ok {
		t.Error("the printed password does not match the stored hash")
	}

	var dep appsv1.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	args := dep.Spec.Template.Spec.Containers[0].Args
	if !slices.Contains(args, "--users-enabled=true") || !slices.Contains(args, "--external-url="+testUIURL) || !slices.Contains(args, "--addr=:8080") {
		t.Errorf("ichor-ui args = %v", args)
	}

	assertControllerIssuerFlags(t, k, args)

	if len(dep.Spec.Template.Spec.Volumes) != 2 || len(dep.Spec.Template.Spec.Containers[0].VolumeMounts) != 2 {
		t.Errorf("signing and tls volumes missing: %+v", dep.Spec.Template.Spec)
	}

	var svc corev1.Service
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testUIServiceName}, &svc); err != nil || len(svc.Spec.Ports) != 2 {
		t.Errorf("service: %v %+v", err, svc.Spec.Ports)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatal(err)
	}

	if a := cluster.Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled || a.Ichor.URL != testUIURL {
		t.Errorf("spec.authentication = %+v", a)
	}

	// The password is on stderr once and nowhere else: not stdout, not a file under HOME.
	if strings.Contains(stdout.String(), pw) {
		t.Error("the admin password reached stdout")
	}

	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, rerr := os.ReadFile(path); rerr == nil && bytes.Contains(data, []byte(pw)) {
				t.Errorf("the admin password was written to %s", path)
			}
		}

		return nil
	})

	if len(updateOrder) == 0 {
		t.Error("the Deployment was never updated")
	}
}

// initOptsWithFlags is validInitOptions with the given init flags applied, as cobra applies them. It
// binds the flags to the fixture itself, so the flags' defaults are overwritten by the fixture
// afterwards, and the parse changes only the flags named in args.
func initOptsWithFlags(t *testing.T, args ...string) initOptions {
	t.Helper()

	var opts initOptions

	var wait time.Duration

	flags := pflag.NewFlagSet("init", pflag.ContinueOnError)
	bindInitFlags(flags, &opts, &wait)

	opts = validInitOptions()
	opts.AllowUnauthenticated = false // the flag's default: sign-in on

	if err := flags.Parse(args); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}

	return opts
}

// A Create of the credentials Secret that times out in etcd but went through is retried, and the
// AlreadyExists the retry meets counts as success.
func TestRunInit_UIURLRetriesAnEtcdTimeoutOnTheCredentialsSecret(t *testing.T) {
	withTempHome(t)
	shrinkInitRetry(t)

	timeouts := 0

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() != ichorauth.CredentialsSecretName || timeouts > 0 {
				return c.Create(ctx, obj, opts...)
			}

			// The write lands, the answer is lost; a copy keeps obj as a timed-out client leaves it.
			if err := c.Create(ctx, obj.DeepCopyObject().(client.Object), opts...); err != nil {
				return err
			}

			timeouts++

			return apierrors.NewInternalError(errors.New("etcdserver: request timed out"))
		},
	}).Build()

	deps, _, _ := newFakeInitDeps(t, k)

	var stderr bytes.Buffer

	deps.Stderr = &stderr

	opts := validInitOptions()
	opts.UIURL, opts.AdminUser = testUIURL, testAdminName
	opts.AllowUnauthenticated = false

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	if timeouts != 1 || !strings.Contains(stderr.String(), "retrying in") {
		t.Errorf("the timeout was not retried (timeouts=%d):\n%s", timeouts, stderr.String())
	}
}

// An Update that times out but lands bumps the resourceVersion, so its retry meets a Conflict;
// init re-Gets, redoes its edit and updates again.
func TestRunInit_UIURLRetriesAnUpdateThatLandedBeforeTheTimeout(t *testing.T) {
	withTempHome(t)
	shrinkInitRetry(t)

	timeouts := 0

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if obj.GetName() != testPrefixedUIDeployment || timeouts > 0 {
				return c.Update(ctx, obj, opts...)
			}

			if err := c.Update(ctx, obj.DeepCopyObject().(client.Object), opts...); err != nil {
				return err
			}

			timeouts++

			return apierrors.NewInternalError(errors.New("etcdserver: request timed out"))
		},
	}).Build()

	deps, _, _ := newFakeInitDeps(t, k)

	opts := validInitOptions()
	opts.UIURL, opts.AdminUser = testUIURL, testAdminName
	opts.AllowUnauthenticated = false

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	var ui appsv1.Deployment
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &ui); err != nil {
		t.Fatal(err)
	}

	n := 0

	for _, c := range ui.Spec.Template.Spec.Containers {
		n += strings.Count(strings.Join(c.Args, " "), "--users-enabled=true")
	}

	if timeouts != 1 || n != 1 {
		t.Errorf("timeouts=%d, --users-enabled=true x%d; want 1 and 1", timeouts, n)
	}
}

// TestRunInit_NoFlagsTurnsSignInOnAtTheDefaultIssuer: an init with no sign-in flags leaves ichor-ui
// requiring sign-in, with the first admin created and the issuer at defaultUIURL.
func TestRunInit_NoFlagsTurnsSignInOnAtTheDefaultIssuer(t *testing.T) {
	withTempHome(t)

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()
	deps, _, _ := newFakeInitDeps(t, k)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	if err := runInit(context.Background(), initOptsWithFlags(t), deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	ctx := context.Background()

	var u ichorv1alpha1.IchorUser
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); err != nil {
		t.Fatalf("the default admin was not created: %v", err)
	}

	var dep appsv1.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	args := dep.Spec.Template.Spec.Containers[0].Args
	if !slices.Contains(args, "--users-enabled=true") || !slices.Contains(args, "--external-url="+defaultUIURL) || slices.Contains(args, "--allow-unauthenticated=true") {
		t.Errorf("ichor-ui args = %v, want sign-in on at %s", args, defaultUIURL)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatal(err)
	}

	if a := cluster.Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled || a.Ichor.URL != defaultUIURL {
		t.Errorf("spec.authentication = %+v, want the issuer %s", a, defaultUIURL)
	}
}

// TestRunInit_AllowUnauthenticatedKeepsIchorUIOpenWithoutSignIn: the explicit flag is the only way to
// an ichor-ui with no sign-in; it creates no admin and leaves spec.authentication unset. The open
// flag reaches the Deployment through the install manifest (TestInitStartsIchorUIWithAnExplicitAllowUnauthenticated).
func TestRunInit_AllowUnauthenticatedKeepsIchorUIOpenWithoutSignIn(t *testing.T) {
	withTempHome(t)

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()
	deps, _, _ := newFakeInitDeps(t, k)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	if err := runInit(context.Background(), initOptsWithFlags(t, "--allow-unauthenticated"), deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	ctx := context.Background()

	var u ichorv1alpha1.IchorUser
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); err == nil {
		t.Error("an admin was created although sign-in is off")
	}

	var dep appsv1.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	if args := dep.Spec.Template.Spec.Containers[0].Args; slices.Contains(args, "--users-enabled=true") {
		t.Errorf("ichor-ui args = %v, want no users", args)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := k.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatal(err)
	}

	if a := cluster.Spec.Authentication; a != nil && a.Ichor != nil && a.Ichor.Enabled {
		t.Errorf("spec.authentication = %+v, want none", a)
	}

	if !strings.Contains(stderr.String(), "no sign-in (--allow-unauthenticated)") {
		t.Errorf("no warning about the open ichor-ui:\n%s", stderr.String())
	}
}

func TestRunInit_UsersFlagsAreCheckedBeforeAnythingHappens(t *testing.T) {
	for name, mutate := range map[string]func(*initOptions){
		"http url":                 func(o *initOptions) { o.UIURL = "http://ichor.example.com" },
		"url with a path query":    func(o *initOptions) { o.UIURL = "https://ichor.example.com/?a=b" },
		"old contract":             func(o *initOptions) { o.UIURL, o.VersionContract = testUIURL, ver113 },
		"old contract, default":    func(o *initOptions) { o.VersionContract = ver113 },
		"bad admin user name":      func(o *initOptions) { o.UIURL, o.AdminUser = testUIURL, "Root User" },
		"ui-url with no sign-in":   func(o *initOptions) { o.UIURL, o.AllowUnauthenticated = testUIURL, true },
		"tls flag with no sign-in": func(o *initOptions) { o.UITLSSecret, o.AllowUnauthenticated = "ichor-ui-cert", true },
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)

			k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
			deps, maint, _ := newFakeInitDeps(t, k)

			opts := validInitOptions()
			opts.AllowUnauthenticated = false
			mutate(&opts)

			if err := runInit(context.Background(), opts, deps); err == nil {
				t.Fatal("expected an error")
			}

			if maint.callCount != 0 {
				t.Error("the node was configured although the flags were invalid")
			}
		})
	}
}

// TestRunInit_MissingUIObjectsLeaveNothingHalfDone: whichever object init needs is
// absent (or unlabelled), it fails before creating the Secret or the admin user.
func TestRunInit_MissingUIObjectsLeaveNothingHalfDone(t *testing.T) {
	for name, drop := range map[string]func(client.Object) bool{
		"no ichor-ui Deployment": func(o client.Object) bool { return o.GetName() == testPrefixedUIDeployment },
		"no controller":          func(o client.Object) bool { return o.GetName() == testControllerDeployment },
		"no ichor-ui Service":    func(o client.Object) bool { return o.GetName() == testUIServiceName },
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)

			var objs []client.Object

			for _, o := range uiObjects() {
				if !drop(o) {
					objs = append(objs, o)
				}
			}

			k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
			deps, _, _ := newFakeInitDeps(t, k)

			var stdout, stderr bytes.Buffer

			deps.Stdout, deps.Stderr = &stdout, &stderr

			opts := validInitOptions()
			opts.UIURL, opts.AllowUnauthenticated = testUIURL, false

			if err := runInit(context.Background(), opts, deps); err == nil {
				t.Fatal("expected an error")
			}

			ctx := context.Background()

			var users ichorv1alpha1.IchorUserList
			if err := k.List(ctx, &users); err != nil || len(users.Items) != 0 {
				t.Errorf("an admin user was created: %v %v", err, users.Items)
			}

			var sec corev1.Secret
			if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName}, &sec); !apierrors.IsNotFound(err) {
				t.Errorf("the credentials Secret was created: %v", err)
			}

			if strings.Contains(stderr.String(), "must change it at the first sign-in") {
				t.Error("a password was printed although nothing was created")
			}
		})
	}
}

func TestPrepareUITargets_FindsByLabelNotName(t *testing.T) {
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiObjects()...).Build()

	tg, err := prepareUITargets(context.Background(), k, ichorControllerNamespace, installer.UISettings{ExternalURL: testUIURL})
	if err != nil {
		t.Fatal(err)
	}

	if tg.ui.Name != testPrefixedUIDeployment || tg.controller.Name != testControllerDeployment || tg.svc.Name != testUIServiceName {
		t.Errorf("found %s, %s, %s", tg.ui.Name, tg.controller.Name, tg.svc.Name)
	}
}

func assertControllerIssuerFlags(t *testing.T, k client.Client, uiArgs []string) {
	t.Helper()

	for _, a := range uiArgs {
		if strings.HasPrefix(a, "--internal-jwks-uri=") {
			t.Errorf("ichor-ui args pin the ClusterIP: %v", uiArgs)
		}
	}

	var mgr appsv1.Deployment
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testControllerDeployment}, &mgr); err != nil {
		t.Fatal(err)
	}

	if margs := mgr.Spec.Template.Spec.Containers[0].Args; !slices.Contains(margs, "--user-issuer") || !slices.Contains(margs, "--oidc-refresh-lifetime=12h") || !slices.Contains(margs, "--leader-elect") {
		t.Errorf("controller args = %v", margs)
	}
}
