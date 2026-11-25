package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorauth"
)

const argUsersEnabled = "--users-enabled=true"

// upgradeCluster is a fake cluster with ichor-ui and the controller, as an install from before sign-in became
// the default has them, and a TalosCluster at the given version contract.
func upgradeCluster(t *testing.T, contract string, extra ...client.Object) client.Client {
	t.Helper()

	cluster := newTestCluster()
	cluster.Namespace, cluster.Spec.VersionContract = ichorControllerNamespace, contract

	objs := slices.Concat(uiObjects(), []client.Object{cluster}, extra)

	return reviewAs(true, fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build())
}

// finishedSignIn is what a completed sign-in leaves besides ichor-ui: the first administrator and its password.
func finishedSignIn() []client.Object {
	return []client.Object{
		&ichorv1alpha1.IchorUser{Namespace: ichorControllerNamespace, Name: defaultAdminUser, Spec: ichorv1alpha1.IchorUserSpec{Roles: []string{ichorauth.RoleAdmin}}},
		&corev1.Secret{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName, Data: map[string][]byte{defaultAdminUser: []byte("{}")}},
	}
}

func uiDeploymentArgs(t *testing.T, k client.Client) []string {
	t.Helper()

	var dep appsv1.Deployment
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	return dep.Spec.Template.Spec.Containers[0].Args
}

// TestUpgradeSignIn_AnInstallWithNoSignInEndsWithSignInOn: the upgrade of an ichor-ui that has none creates the
// first administrator, switches ichor-ui, sets the authenticator on the cluster, and prints the password once,
// after the upgrade itself, whether or not the upgrade worked.
func TestUpgradeSignIn_AnInstallWithNoSignInEndsWithSignInOn(t *testing.T) {
	for name, startErr := range map[string]error{"the upgrade works": nil, "the upgrade fails": errors.New("no permission")} {
		t.Run(name, func(t *testing.T) {
			k := upgradeCluster(t, testVersionContract)

			var errw bytes.Buffer

			started := 0
			err := upgradeWithSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{}, true, func() error { return nil }, func() error {
				started++

				if args := uiDeploymentArgs(t, k); !slices.Contains(args, argUsersEnabled) {
					t.Errorf("the upgrade started before sign-in was on: %v", args)
				}

				return startErr
			})
			if !errors.Is(err, startErr) {
				t.Fatalf("err = %v, want %v", err, startErr)
			}

			if started != 1 {
				t.Fatalf("the upgrade started %d times", started)
			}

			var u ichorv1alpha1.IchorUser
			if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); err != nil {
				t.Fatalf("no administrator: %v", err)
			}

			args := uiDeploymentArgs(t, k)
			if !slices.Contains(args, "--external-url="+defaultUIURL) {
				t.Errorf("ichor-ui args = %v, want sign-in at %s", args, defaultUIURL)
			}

			var c ichorv1alpha1.TalosCluster
			if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: newTestCluster().Name}, &c); err != nil {
				t.Fatal(err)
			}

			if a := c.Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled || a.Ichor.URL != defaultUIURL {
				t.Errorf("spec.authentication = %+v, want the issuer %s", a, defaultUIURL)
			}

			out := errw.String()
			if n := strings.Count(out, "Password for admin"); n != 1 {
				t.Errorf("the password was printed %d times:\n%s", n, out)
			}

			if _, after, _ := strings.Cut(out, "Password for admin"); strings.Contains(after, authRolloutNotice) {
				t.Errorf("the password is buried above other output:\n%s", out)
			}
		})
	}
}

// TestUpgradeSignIn_AsksBeforeAnythingIsWritten: without --yes the answer decides, and anything but yes leaves
// the cluster as it was and does not start the upgrade.
func TestUpgradeSignIn_AsksBeforeAnythingIsWritten(t *testing.T) {
	for name, answer := range map[string]string{"no": "n\n", "empty input": ""} {
		t.Run(name, func(t *testing.T) {
			k := upgradeCluster(t, testVersionContract)

			var errw bytes.Buffer

			err := upgradeWithSignIn(context.Background(), k, strings.NewReader(answer), &errw, initOptions{}, false, func() error { return nil }, func() error {
				t.Error("the upgrade started without consent")

				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "aborted") {
				t.Fatalf("err = %v, want an abort", err)
			}

			if args := uiDeploymentArgs(t, k); slices.Contains(args, argUsersEnabled) {
				t.Errorf("ichor-ui was switched without consent: %v", args)
			}

			var users ichorv1alpha1.IchorUserList
			if err := k.List(context.Background(), &users); err != nil || len(users.Items) != 0 {
				t.Errorf("an administrator was created without consent: %v %v", err, users.Items)
			}

			if !strings.Contains(errw.String(), "APIServerRestartRequired") {
				t.Errorf("the question does not name the kube-apiserver restart:\n%s", errw.String())
			}
		})
	}
}

// TestUpgradeSignIn_LeavesWhatHasSignInAlone: an ichor-ui with users on is not touched, and a sign-in flag that
// would then change nothing is refused. A cluster below v1.14 cannot carry the issuer, so it is left out of the
// authenticator step; the users step does not depend on it.
func TestUpgradeSignIn_LeavesWhatHasSignInAlone(t *testing.T) {
	usersOn := uiObjects()
	usersOn[0].(*appsv1.Deployment).Spec.Template.Spec.Containers[0].Args = []string{argUsersEnabled, "--external-url=" + testUIURL}

	cluster := newTestCluster()
	cluster.Namespace = ichorControllerNamespace
	cluster.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: testUIURL}}
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(usersOn, append(finishedSignIn(), cluster)...)...).Build()

	var errw bytes.Buffer

	user, password, err := ensureSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{}, false)
	if err != nil || user != "" || password != "" || errw.Len() != 0 {
		t.Fatalf("ensureSignIn = %q %q %v with output %q, want nothing", user, password, err, errw.String())
	}

	if _, _, err := ensureSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{UIURL: "https://other.example.com"}, true); err == nil ||
		!strings.Contains(err.Error(), "already has sign-in on") {
		t.Errorf("--ui-url with sign-in on: %v", err)
	}

	old := upgradeCluster(t, ver113)

	if _, _, err := ensureSignIn(context.Background(), old, strings.NewReader(""), &errw, initOptions{}, true); err != nil {
		t.Fatal(err)
	}

	if args := uiDeploymentArgs(t, old); !slices.Contains(args, argUsersEnabled) {
		t.Errorf("a cluster below v1.14 kept ichor-ui from sign-in: %v", args)
	}

	var c ichorv1alpha1.TalosCluster
	if err := old.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: newTestCluster().Name}, &c); err != nil {
		t.Fatal(err)
	}

	if a := c.Spec.Authentication; a != nil && a.Ichor != nil && a.Ichor.Enabled {
		t.Errorf("the authenticator was set on a cluster that cannot carry it: %+v", a)
	}
}

// TestUpgradeIchorRefusesBeforeItWrites: a downgrade target, or a stored patch that holds a retired field, makes
// `ichor upgrade ichor` refuse with sign-in still off, and the cluster is never written.
func TestUpgradeIchorRefusesBeforeItWrites(t *testing.T) {
	retired := &unstructured.Unstructured{}
	if err := json.Unmarshal([]byte(`{"apiVersion":"ichor.maesh.dev/v1alpha1","kind":"TalosConfigPatch",
		"metadata":{"name":"reg","namespace":"`+ichorControllerNamespace+`"},
		"spec":{"documents":[{"kind":"v1alpha1","config":{"machine":{"registries":{"config":{"r.example":{"tls":{"clientIdentity":{"crt":"x"}}}}}}}}]}}`), &retired.Object); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		target string
		extra  []client.Object
		want   string
	}{
		"a downgrade":            {"v0.2.3", nil, "cannot downgrade ichor from v0.2.4 to v0.2.3"},
		"a retired stored field": {humanLatest, []client.Object{retired}, "move it out of the patch and set it on the node outside Ichor (ADR 0152) before upgrading"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inst := &ichorv1alpha1.IchorInstallation{
				Spec:   ichorv1alpha1.IchorInstallationSpec{Version: runningVersion},
				Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: runningVersion},
			}
			inst.Name = ichorv1alpha1.IchorInstallationName
			cluster := newTestCluster()
			cluster.Namespace, cluster.Spec.VersionContract = ichorControllerNamespace, testVersionContract

			scheme := newTestScheme(t)

			writes := 0
			count := func() { writes++ }
			k := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(slices.Concat(uiObjects(), []client.Object{cluster, inst})...).
				WithInterceptorFuncs(interceptor.Funcs{
					// The stored patches are served as written, as the API server does for an unstructured list: the
					// typed TalosConfigPatch in cliScheme would drop the field the Go type no longer has.
					List: func(ctx context.Context, c client.WithWatch, l client.ObjectList, opts ...client.ListOption) error {
						if ul, ok := l.(*unstructured.UnstructuredList); ok && ul.GetKind() == "TalosConfigPatchList" {
							for _, o := range tc.extra {
								ul.Items = append(ul.Items, *o.(*unstructured.Unstructured).DeepCopy())
							}

							return nil
						}

						return c.List(ctx, l, opts...)
					},
					Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
						count()
						return c.Create(ctx, o, opts...)
					},
					Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
						count()
						return c.Update(ctx, o, opts...)
					},
					Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
						count()
						return c.Patch(ctx, o, p, opts...)
					},
					Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
						count()
						return c.Delete(ctx, o, opts...)
					},
					SubResource: func(c client.WithWatch, s string) client.SubResourceClient { count(); return c.SubResource(s) },
				}).Build()

			started := false
			err := upgradeWithSignIn(context.Background(), k, strings.NewReader(""), io.Discard, initOptions{}, true,
				func() error { return preflightIchorUpgrade(context.Background(), k, tc.target) },
				func() error { started = true; return nil })

			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a refusal containing %q, got %v", tc.want, err)
			}

			if started || writes != 0 {
				t.Errorf("the refusal came after a write: started=%v, writes=%d", started, writes)
			}

			if args := uiDeploymentArgs(t, k); slices.Contains(args, argUsersEnabled) {
				t.Errorf("sign-in was switched on: %v", args)
			}
		})
	}
}

// failFirstPatchOfClusters refuses every TalosCluster patch while *failing is set.
func failFirstPatchOfClusters(base client.WithWatch, failing *bool) client.Client {
	return interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, o ...client.PatchOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosCluster); ok && *failing {
				return apierrors.NewForbidden(schema.GroupResource{Resource: "talosclusters"}, obj.GetName(), nil)
			}

			return c.Patch(ctx, obj, p, o...)
		},
	})
}

// TestUpgradeSignIn_RerunAfterAFailedAuthenticatorSetsItOnEveryCluster: the first run switches ichor-ui and creates the
// administrator, then the TalosCluster patch is refused. The re-run, with ichor-ui signed in, finishes the clusters
// and keeps the administrator.
func TestUpgradeSignIn_RerunAfterAFailedAuthenticatorSetsItOnEveryCluster(t *testing.T) {
	second := newTestCluster()
	second.Name, second.Namespace, second.Spec.VersionContract = "second", ichorControllerNamespace, testVersionContract

	base := upgradeCluster(t, testVersionContract, second).(client.WithWatch)
	failing := true
	k := failFirstPatchOfClusters(base, &failing)

	run := func() (string, error) {
		var errw bytes.Buffer

		err := upgradeWithSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{}, true, func() error { return nil }, func() error { return nil })

		return errw.String(), err
	}

	if _, err := run(); err == nil {
		t.Fatal("the first run succeeded although the TalosCluster patch was refused")
	}

	failing = false

	out, err := run()
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}

	if strings.Contains(out, "Password for admin") || strings.Contains(out, "already exists") {
		t.Errorf("the second run made or refused an administrator:\n%s", out)
	}

	var list ichorv1alpha1.TalosClusterList
	if err := base.List(context.Background(), &list); err != nil || len(list.Items) != 2 {
		t.Fatalf("clusters: %v %d", err, len(list.Items))
	}

	for _, c := range list.Items {
		if a := c.Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled || a.Ichor.URL != defaultUIURL {
			t.Errorf("cluster %s: spec.authentication = %+v, want the issuer %s", c.Name, a, defaultUIURL)
		}
	}
}

// TestUpgradeSignIn_RerunReusesAnAdministratorAFailedRunLeft: the first run creates the administrator and then fails
// to switch ichor-ui; the re-run keeps that administrator instead of refusing it.
func TestUpgradeSignIn_RerunReusesAnAdministratorAFailedRunLeft(t *testing.T) {
	base := upgradeCluster(t, testVersionContract).(client.WithWatch)
	failing := true
	k := interceptor.NewClient(base, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.UpdateOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok && failing {
				return errors.New("the apiserver is gone")
			}

			return c.Update(ctx, obj, o...)
		},
	})

	run := func() error {
		return upgradeWithSignIn(context.Background(), k, strings.NewReader(""), io.Discard, initOptions{}, true, func() error { return nil }, func() error { return nil })
	}

	if err := run(); err == nil {
		t.Fatal("the first run succeeded although ichor-ui could not be switched")
	}

	failing = false

	if err := run(); err != nil {
		t.Fatalf("second run: %v", err)
	}

	if args := uiDeploymentArgs(t, base); !slices.Contains(args, argUsersEnabled) {
		t.Errorf("ichor-ui was not switched by the re-run: %v", args)
	}
}

// TestUpgradeSignIn_RefusesBeforeAnyWriteWithoutTheAuthenticationRight: the read-only access check comes first.
func TestUpgradeSignIn_RefusesBeforeAnyWriteWithoutTheAuthenticationRight(t *testing.T) {
	base := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(slices.Concat(uiObjects(), []client.Object{func() client.Object {
		c := newTestCluster()
		c.Namespace, c.Spec.VersionContract = ichorControllerNamespace, testVersionContract

		return c
	}()})...).Build()

	_, _, err := ensureSignIn(context.Background(), reviewAs(false, base), strings.NewReader(""), io.Discard, initOptions{}, true)
	if err == nil || !strings.Contains(err.Error(), "talosclusters/authentication") {
		t.Fatalf("err = %v, want a refusal naming talosclusters/authentication", err)
	}

	if args := uiDeploymentArgs(t, base); slices.Contains(args, argUsersEnabled) {
		t.Errorf("ichor-ui was switched before the refusal: %v", args)
	}

	var users ichorv1alpha1.IchorUserList
	if err := base.List(context.Background(), &users); err != nil || len(users.Items) != 0 {
		t.Errorf("an administrator was created before the refusal: %v %v", err, users.Items)
	}
}

// signedInUI turns the fake ichor-ui into an install that has sign-in on, with the operator's kube-api settings.
func signedInUI(t *testing.T, k client.Client) {
	t.Helper()

	var dep appsv1.Deployment
	if err := k.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}, &dep); err != nil {
		t.Fatal(err)
	}

	c := &dep.Spec.Template.Spec.Containers[0]
	c.Args = append(c.Args, argUsersEnabled, "--external-url="+defaultUIURL, "--kube-api-server-url=https://kube.example:6443")

	if err := k.Update(context.Background(), &dep); err != nil {
		t.Fatal(err)
	}
}

func aliceAdmin() []client.Object {
	return []client.Object{
		&ichorv1alpha1.IchorUser{Namespace: ichorControllerNamespace, Name: testUserAlice, Spec: ichorv1alpha1.IchorUserSpec{Roles: []string{ichorauth.RoleAdmin}}},
		&corev1.Secret{Namespace: ichorControllerNamespace, Name: ichorauth.CredentialsSecretName, Data: map[string][]byte{testUserAlice: []byte("{}")}},
	}
}

// TestUpgradeSignIn_AHealthyInstallWithAnotherAdminIsLeftAlone: a signed-in install whose administrator is not
// called admin is not a part-failed run: no admin is made, ichor-ui keeps its kube-api settings, and a cluster whose
// operator turned the issuer off stays off.
func TestUpgradeSignIn_AHealthyInstallWithAnotherAdminIsLeftAlone(t *testing.T) {
	off := newTestCluster()
	off.Namespace, off.Spec.VersionContract = ichorControllerNamespace, testVersionContract
	off.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: false, URL: defaultUIURL}}

	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(slices.Concat(uiObjects(), aliceAdmin(), []client.Object{off})...).Build()
	signedInUI(t, k)

	before := slices.Clone(uiDeploymentArgs(t, k))

	plan, _, err := signInPlanFor(context.Background(), k, initOptions{})
	if err != nil || plan != nil {
		t.Fatalf("plan = %+v, err = %v, want none", plan, err)
	}

	if _, _, err := ensureSignIn(context.Background(), reviewAs(true, k), strings.NewReader(""), io.Discard, initOptions{}, true); err != nil {
		t.Fatal(err)
	}

	var users ichorv1alpha1.IchorUserList
	if err := k.List(context.Background(), &users); err != nil || len(users.Items) != 1 {
		t.Errorf("users = %v, err = %v, want only alice", users.Items, err)
	}

	if args := uiDeploymentArgs(t, k); !slices.Equal(args, before) || !slices.Contains(args, "--kube-api-server-url=https://kube.example:6443") {
		t.Errorf("ichor-ui args = %v, want %v", args, before)
	}

	var c ichorv1alpha1.TalosCluster
	if err := k.Get(context.Background(), client.ObjectKeyFromObject(off), &c); err != nil || c.Spec.Authentication.Ichor.Enabled {
		t.Errorf("the issuer was turned back on: %v %+v", err, c.Spec.Authentication)
	}
}
