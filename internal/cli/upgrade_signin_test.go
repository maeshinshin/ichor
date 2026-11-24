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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const argUsersEnabled = "--users-enabled=true"

// upgradeCluster is a fake cluster with ichor-ui and the controller, as an install from before sign-in became
// the default has them, and a TalosCluster at the given version contract.
func upgradeCluster(t *testing.T, contract string, extra ...client.Object) client.Client {
	t.Helper()

	cluster := newTestCluster()
	cluster.Namespace, cluster.Spec.VersionContract = ichorControllerNamespace, contract

	objs := slices.Concat(uiObjects(), []client.Object{cluster}, extra)

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
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
	k := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(usersOn, cluster)...).Build()

	var errw bytes.Buffer

	user, password, err := ensureSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{}, false)
	if err != nil || user != "" || password != "" || errw.Len() != 0 {
		t.Fatalf("ensureSignIn = %q %q %v with output %q, want nothing", user, password, err, errw.String())
	}

	if _, _, err := ensureSignIn(context.Background(), k, strings.NewReader(""), &errw, initOptions{UIURL: testUIURL}, true); err == nil ||
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

			// No typed TalosConfigPatch: the fake keeps the stored object as written, as the API server does for an
			// unstructured read, instead of dropping the field the Go type no longer has.
			scheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(scheme))
			scheme.AddKnownTypes(ichorv1alpha1.GroupVersion, &ichorv1alpha1.IchorInstallation{}, &ichorv1alpha1.IchorInstallationList{}, &ichorv1alpha1.TalosCluster{}, &ichorv1alpha1.TalosClusterList{}, &ichorv1alpha1.IchorUser{}, &ichorv1alpha1.IchorUserList{})
			metav1.AddToGroupVersion(scheme, ichorv1alpha1.GroupVersion)

			writes := 0
			count := func() { writes++ }
			k := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(slices.Concat(uiObjects(), []client.Object{cluster, inst}, tc.extra)...).
				WithInterceptorFuncs(interceptor.Funcs{
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
