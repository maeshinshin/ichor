package installer

import (
	"context"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	uiAddr       = "--addr=:8443"
	argIssuerURL = "--external-url=https://i.example.com"
	argUsersOn   = "--users-enabled=true"
)

func uiWith(args []string, env ...corev1.EnvVar) *appsv1.Deployment {
	return &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: UIContainer, Args: args, Env: env}},
	}}}}
}

// TestUpgradeDropsTheOpenMode runs a live ichor-ui through the installer's read-and-customize round, the
// path of every upgrade. However it was left open (the flag, the variable, or both, with or without a
// sign-in), what comes out starts without the open mode, and the notice is for the installs that had it.
func TestUpgradeDropsTheOpenMode(t *testing.T) {
	open := corev1.EnvVar{Name: envAllowUnauthenticated, Value: "yes"}

	cases := []struct {
		name    string
		dep     *appsv1.Deployment
		wasOpen bool
	}{
		{"no sign-in, no flag (pre-0108)", uiWith([]string{uiAddr}), false},
		{"flag", uiWith([]string{uiAddr, argAllowAnonymous}), true},
		{"variable", uiWith([]string{uiAddr}, open), true},
		{"flag and variable", uiWith([]string{uiAddr, argAllowAnonymous}, open), true},
		{"flag beside Ichor users", uiWith([]string{uiAddr, argAllowAnonymous, argUsersOn, argIssuerURL}), true},
		{"flag beside external OIDC", uiWith([]string{uiAddr, argAllowAnonymous}, corev1.EnvVar{Name: "ICHOR_OIDC_ISSUER", Value: "https://idp"}), true},
		{"ichor users", uiWith([]string{uiAddr, argUsersOn, argIssuerURL}), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ReadUISettings(tc.dep, nil)
			if s.WasOpen != tc.wasOpen {
				t.Fatalf("WasOpen = %v, want %v", s.WasOpen, tc.wasOpen)
			}

			if err := CustomizeUI(tc.dep, s); err != nil {
				t.Fatal(err)
			}

			c := tc.dep.Spec.Template.Spec.Containers[0]
			if slices.ContainsFunc(c.Args, func(a string) bool { return strings.HasPrefix(a, "--allow-unauthenticated") }) {
				t.Errorf("the open flag survived the upgrade: %v", c.Args)
			}

			if slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == envAllowUnauthenticated }) {
				t.Errorf("the open variable survived the upgrade: %v", c.Env)
			}
		})
	}
}

// TestUpgradeRefusesALiveUIWithNoSignIn: the ichor-ui this release installs does not start without a sign-in,
// so the installer stops, changing nothing, at one that has none, whatever else it carries (the removed flag
// included), and goes on with each that has Ichor users or an external provider. The CLI turns sign-in on
// before it starts an upgrade (internal/cli's ensureSignIn), so this is the stop for every other way an
// upgrade starts.
func TestUpgradeRefusesALiveUIWithNoSignIn(t *testing.T) {
	oidc := corev1.EnvVar{Name: "ICHOR_OIDC_ISSUER", Value: "https://idp"}

	for name, tc := range map[string]struct {
		dep     *appsv1.Deployment
		refused bool
	}{
		"nothing":                                {uiWith([]string{uiAddr}), true},
		"the removed flag":                       {uiWith([]string{uiAddr, argAllowAnonymous}), true},
		"the removed variable":                   {uiWith([]string{uiAddr}, corev1.EnvVar{Name: envAllowUnauthenticated, Value: "yes"}), true},
		"an issuer URL (users are what it sets)": {uiWith([]string{uiAddr, argIssuerURL}), false},
		"Ichor users":                            {uiWith([]string{uiAddr, argUsersOn, argIssuerURL}), false},
		"OIDC by variable":                       {uiWith([]string{uiAddr}, oidc), false},
		"OIDC by flag":                           {uiWith([]string{uiAddr, "--oidc-issuer=https://idp"}), false},
	} {
		t.Run(name, func(t *testing.T) {
			ui := tc.dep
			ui.Name, ui.Namespace = "ichor-ui", "ichor-system"
			ui.Labels = map[string]string{"app.kubernetes.io/component": "ui"}

			ctrl := uiWith([]string{"--leader-elect"})
			ctrl.Name, ctrl.Namespace = "ichor-controller", "ichor-system"
			ctrl.Spec.Template.Spec.Containers[0].Name = ControllerContainer

			scheme := runtime.NewScheme()
			_ = appsv1.AddToScheme(scheme)

			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ui, ctrl).Build()

			var objs []*unstructured.Unstructured

			for _, d := range []*appsv1.Deployment{ui, ctrl} {
				raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(d)
				if err != nil {
					t.Fatal(err)
				}

				u := &unstructured.Unstructured{Object: raw}
				u.SetKind(kindDeployment)
				u.SetAPIVersion("apps/v1")
				objs = append(objs, u)
			}

			_, _, err := customizeFromLive(context.Background(), c, objs)
			if (err != nil) != tc.refused {
				t.Fatalf("customizeFromLive error = %v, want refused=%v", err, tc.refused)
			}

			if err != nil && (!strings.Contains(err.Error(), "ichor upgrade ichor") || !strings.Contains(err.Error(), "no sign-in")) {
				t.Errorf("the refusal does not say what to do: %v", err)
			}
		})
	}
}
