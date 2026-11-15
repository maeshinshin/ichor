package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const testKubeAPIURL = "https://k8s.example.com:6443"

// TestAdoptSignIn_TLSAndKubeconfigFlagsReachIchorUI: with no way to leave ichor-ui open, an adopter who
// serves it on their own https address needs the flags init has. They are applied by the same users step,
// so ichor-ui gets the certificate Secret and the kubeconfig flags the chart's values would give it.
func TestAdoptSignIn_TLSAndKubeconfigFlagsReachIchorUI(t *testing.T) {
	f := signInFlow(t)
	cert, key, _ := writeTestKeyPair(t)
	f.deps.UIURL = testUIURL
	f.deps.UITLSCert, f.deps.UITLSKey = cert, key
	f.deps.KubeAPIServerURL, f.deps.KubeAPITLSServerName = testKubeAPIURL, "k8s.example.com"

	if err := f.runSignIn(); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	args := f.uiArgs(t)
	for _, want := range []string{
		"--users-enabled=true", "--external-url=" + testUIURL, "--tls-cert-file=/etc/ichor/tls/tls.crt",
		"--kube-api-server-url=" + testKubeAPIURL, "--kube-api-tls-server-name=k8s.example.com",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("ichor-ui args %v lack %s", args, want)
		}
	}

	var sec corev1.Secret
	if err := f.env.k8s.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: uiTLSStoredSecretName}, &sec); err != nil {
		t.Fatalf("the certificate was not stored: %v", err)
	}

	if sec.Type != corev1.SecretTypeTLS || len(sec.Data[corev1.TLSPrivateKeyKey]) == 0 {
		t.Errorf("Secret %s = type %s with %d keys, want a kubernetes.io/tls pair", sec.Name, sec.Type, len(sec.Data))
	}
}

// TestAdoptSignIn_TLSFlagsAreCheckedBeforeAnythingIsWritten: a bad pair or URL stops the plan, and the
// refusal names adopt, not init.
func TestAdoptSignIn_TLSFlagsAreCheckedBeforeAnythingIsWritten(t *testing.T) {
	cert, key, _ := writeTestKeyPair(t)

	for name, mutate := range map[string]func(*adoptDeps){
		"cert without key":     func(d *adoptDeps) { d.UITLSCert = cert },
		"key without cert":     func(d *adoptDeps) { d.UITLSKey = key },
		"secret and files":     func(d *adoptDeps) { d.UITLSSecret, d.UITLSCert, d.UITLSKey = "x", cert, key },
		"http kube api url":    func(d *adoptDeps) { d.KubeAPIServerURL = "http://k8s.example.com" },
		"unreadable cert file": func(d *adoptDeps) { d.UITLSCert, d.UITLSKey = "/nonexistent/c.pem", key },
	} {
		t.Run(name, func(t *testing.T) {
			f := signInFlow(t)
			mutate(&f.deps)

			err := f.runSignIn()
			if err == nil {
				t.Fatal("runAdopt succeeded, want a refusal")
			}

			if !strings.HasPrefix(err.Error(), "adopt:") {
				t.Errorf("the refusal does not name adopt: %v", err)
			}

			if ev := f.events(); len(ev) != 0 {
				t.Errorf("something was written: %v", ev)
			}
		})
	}
}

// TestAdoptSignIn_FlagsThatWouldChangeNothingAreRefused: adopt does not touch a Helm-managed Ichor or an
// ichor-ui whose users are already on, so a TLS or kubeconfig flag there would be accepted and do
// nothing. It is refused instead, with what to do.
func TestAdoptSignIn_FlagsThatWouldChangeNothingAreRefused(t *testing.T) {
	usersOn := uiObjects()
	usersOn[0].(*appsv1.Deployment).Spec.Template.Spec.Containers[0].Args = []string{"--users-enabled=true", "--external-url=" + testUIURL}

	for name, tc := range map[string]struct {
		objs []client.Object
		want string
	}{
		"Helm-managed":     {[]client.Object{controllerDeployment(true)}, "ichorUI.users.kubeAPIServerURL"},
		"users already on": {usersOn, "already has sign-in on"},
	} {
		t.Run(name, func(t *testing.T) {
			f := signInFlow(t)

			for _, o := range tc.objs {
				if err := f.env.k8s.Create(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			}

			f.deps.KubeAPIServerURL = testKubeAPIURL

			err := f.runSignIn()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runAdopt = %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}
