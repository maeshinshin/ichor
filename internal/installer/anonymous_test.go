package installer

import (
	"slices"
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

func TestUpgradeKeepsAnInstallWithoutSignInOpenAndSaysSo(t *testing.T) {
	cases := []struct {
		name          string
		dep           *appsv1.Deployment
		wantAllow     bool
		wantInferred  bool
		wantFlagInArg bool
	}{
		{"no sign-in, no flag (pre-0108)", uiWith([]string{uiAddr}), true, true, true},
		{"flag already set", uiWith([]string{uiAddr, "--allow-unauthenticated=true"}), true, false, true},
		{"ichor users", uiWith([]string{uiAddr, "--users-enabled=true", "--external-url=https://i.example.com"}), false, false, false},
		{"external OIDC by env", uiWith([]string{uiAddr}, corev1.EnvVar{Name: "ICHOR_OIDC_ISSUER", Value: "https://idp"}), false, false, false},
		{"no live Deployment (first install)", nil, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ReadUISettings(tc.dep, nil)
			if s.AllowUnauthenticated != tc.wantAllow || s.InferredAnonymous != tc.wantInferred {
				t.Fatalf("allow=%v inferred=%v, want %v %v", s.AllowUnauthenticated, s.InferredAnonymous, tc.wantAllow, tc.wantInferred)
			}

			dep := uiWith([]string{uiAddr})
			if err := CustomizeUI(dep, s); err != nil {
				t.Fatal(err)
			}

			if got := slices.Contains(dep.Spec.Template.Spec.Containers[0].Args, "--allow-unauthenticated=true"); got != tc.wantFlagInArg {
				t.Errorf("flag in args = %v, want %v: %v", got, tc.wantFlagInArg, dep.Spec.Template.Spec.Containers[0].Args)
			}
		})
	}
}
