package installer

import (
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const uiAddr = "--addr=:8443"

func uiWith(args []string, env ...corev1.EnvVar) *appsv1.Deployment {
	return &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
		Containers: []corev1.Container{{Name: UIContainer, Args: args, Env: env}},
	}}}}
}

// TestUpgradeDropsTheOpenMode runs a live ichor-ui through the installer's read-and-customize round, the
// path of every upgrade. However it was left open (the flag, the variable, or both, with or without a
// sign-in), what comes out starts without the open mode, and the notice is for the installs that had it.
func TestUpgradeDropsTheOpenMode(t *testing.T) {
	open := corev1.EnvVar{Name: "ICHOR_ALLOW_UNAUTHENTICATED", Value: "true"}

	cases := []struct {
		name    string
		dep     *appsv1.Deployment
		wasOpen bool
	}{
		{"no sign-in, no flag (pre-0108)", uiWith([]string{uiAddr}), false},
		{"flag", uiWith([]string{uiAddr, "--allow-unauthenticated=true"}), true},
		{"variable", uiWith([]string{uiAddr}, open), true},
		{"flag and variable", uiWith([]string{uiAddr, "--allow-unauthenticated=true"}, open), true},
		{"flag beside Ichor users", uiWith([]string{uiAddr, "--allow-unauthenticated=true", "--users-enabled=true", "--external-url=https://i.example.com"}), true},
		{"flag beside external OIDC", uiWith([]string{uiAddr, "--allow-unauthenticated=true"}, corev1.EnvVar{Name: "ICHOR_OIDC_ISSUER", Value: "https://idp"}), true},
		{"ichor users", uiWith([]string{uiAddr, "--users-enabled=true", "--external-url=https://i.example.com"}), false},
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

			if slices.ContainsFunc(c.Env, func(e corev1.EnvVar) bool { return e.Name == "ICHOR_ALLOW_UNAUTHENTICATED" }) {
				t.Errorf("the open variable survived the upgrade: %v", c.Env)
			}
		})
	}
}
