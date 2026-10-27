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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const testUIURL = "https://ichor.example.com"

func uiObjects() []client.Object {
	return []client.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Namespace: ichorControllerNamespace, Name: uiDeploymentName},
			Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "ichor-ui", Args: []string{"--addr=:8080"}}},
			}}},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: ichorControllerNamespace, Name: uiServiceName},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
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
				if err := c.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: "root"}, &u); err != nil {
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
	opts.UIURL, opts.AdminUser = testUIURL, "root"

	if err := runInit(context.Background(), opts, deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	ctx := context.Background()

	var u ichorv1alpha1.IchorUser
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: "root"}, &u); err != nil || !slices.Equal(u.Spec.Roles, []string{"admin"}) {
		t.Fatalf("admin user: %v %+v", err, u.Spec)
	}

	var sec corev1.Secret
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName}, &sec); err != nil {
		t.Fatal(err)
	}

	cred, err := ichorauth.ParseCredential(sec.Data["root"])
	if err != nil || !cred.MustChange {
		t.Fatalf("credential: %v %+v", err, cred)
	}

	pw := generatedAdminPassword(t, stderr.String())
	if ok, _, err := cred.Verify(pw); err != nil || !ok {
		t.Error("the printed password does not match the stored hash")
	}

	var dep appsv1.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: uiDeploymentName}, &dep); err != nil {
		t.Fatal(err)
	}

	args := dep.Spec.Template.Spec.Containers[0].Args
	if !slices.Contains(args, "--users-enabled=true") || !slices.Contains(args, "--external-url="+testUIURL) || !slices.Contains(args, "--addr=:8080") {
		t.Errorf("ichor-ui args = %v", args)
	}

	if len(dep.Spec.Template.Spec.Volumes) != 2 || len(dep.Spec.Template.Spec.Containers[0].VolumeMounts) != 2 {
		t.Errorf("signing and tls volumes missing: %+v", dep.Spec.Template.Spec)
	}

	var svc corev1.Service
	if err := k.Get(ctx, types.NamespacedName{Namespace: ichorControllerNamespace, Name: uiServiceName}, &svc); err != nil || len(svc.Spec.Ports) != 2 {
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

func TestRunInit_UsersFlagsAreCheckedBeforeAnythingHappens(t *testing.T) {
	for name, mutate := range map[string]func(*initOptions){
		"http url":                  func(o *initOptions) { o.UIURL = "http://ichor.example.com" },
		"url with a path query":     func(o *initOptions) { o.UIURL = "https://ichor.example.com/?a=b" },
		"old contract":              func(o *initOptions) { o.UIURL, o.VersionContract = testUIURL, "v1.13" },
		"admin user without ui-url": func(o *initOptions) { o.AdminUser = "root" },
		"bad admin user name":       func(o *initOptions) { o.UIURL, o.AdminUser = testUIURL, "Root User" },
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)

			k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
			deps, maint, _ := newFakeInitDeps(t, k)

			opts := validInitOptions()
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

func TestRunInit_PasswordIsStillPrintedWhenALaterStepFails(t *testing.T) {
	withTempHome(t)

	// No ichor-ui Deployment in the fixture: enabling users fails after the admin exists.
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	deps, _, _ := newFakeInitDeps(t, k)

	var stdout, stderr bytes.Buffer

	deps.Stdout, deps.Stderr = &stdout, &stderr

	opts := validInitOptions()
	opts.UIURL = testUIURL

	err := runInit(context.Background(), opts, deps)
	if err == nil {
		t.Fatal("expected the missing Deployment to fail")
	}

	pw := generatedAdminPassword(t, stderr.String())
	if strings.Contains(err.Error(), pw) || strings.Contains(stdout.String(), pw) {
		t.Error("the password leaked into the error or stdout")
	}

	var u ichorv1alpha1.IchorUser
	if gerr := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); apierrors.IsNotFound(gerr) {
		t.Error("the default admin user name should be admin")
	}
}
