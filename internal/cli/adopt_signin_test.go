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
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// signInManifest is the part of Ichor's install manifest sign-in edits: the ichor-ui and controller
// Deployments and the ichor-ui Service, named the way kustomize names them.
var signInManifest = fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[3]s
  labels: {%[4]s: %[5]s}
spec:
  selector: {matchLabels: {app: ui}}
  template:
    metadata: {labels: {app: ui}}
    spec:
      containers:
      - {name: %[6]s, image: ui, args: ["--addr=:8080"]}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[2]s
  namespace: %[3]s
  labels: {%[7]s: %[8]s}
spec:
  selector: {matchLabels: {app: controller}}
  template:
    metadata: {labels: {app: controller}}
    spec:
      containers:
      - {name: %[9]s, image: controller, args: ["--leader-elect"]}
---
apiVersion: v1
kind: Service
metadata:
  name: %[10]s
  namespace: %[3]s
  labels: {%[4]s: %[5]s}
spec:
  clusterIP: %[11]s
  ports: [{name: http, port: 8080}]
`, testPrefixedUIDeployment, testControllerDeployment, ichorControllerNamespace, uiComponentLabel, uiComponentValue, uiContainer,
	controllerLabel, controllerLabelValue, controllerContainer, testUIServiceName, testUIClusterIP)

// signInFlow is the no-CNI adopt of noCNIFlow with an Ichor install that carries ichor-ui, and
// sign-in left at its default.
func signInFlow(t *testing.T) *noCNIFlow {
	t.Helper()

	f := newNoCNIFlow(t)
	f.deps.AllowUnauthenticated = false
	f.deps.Ichor = &adoptWriteIchor{Namespace: ichorControllerNamespace, Manifest: []byte(signInManifest)}

	return f
}

func (f *noCNIFlow) runSignIn() error {
	return runAdopt(context.Background(), f.env.cluster.Name, ichorControllerNamespace, f.deps)
}

func (f *noCNIFlow) uiArgs(t *testing.T) []string {
	t.Helper()

	var dep appsv1.Deployment

	key := types.NamespacedName{Namespace: ichorControllerNamespace, Name: testPrefixedUIDeployment}
	if err := f.env.k8s.Get(context.Background(), key, &dep); err != nil {
		t.Fatal(err)
	}

	return dep.Spec.Template.Spec.Containers[0].Args
}

func (f *noCNIFlow) cluster(t *testing.T) ichorv1alpha1.TalosCluster {
	t.Helper()

	var c ichorv1alpha1.TalosCluster
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: ichorControllerNamespace, Name: f.env.cluster.Name}, &c); err != nil {
		t.Fatal(err)
	}

	return c
}

// TestAdoptSignIn_LeavesIchorUIRequiringSignInWithAnAdmin: an adopt that completes, with no sign-in
// flag, leaves ichor-ui requiring sign-in with an administrator, the issuer set on the TalosCluster,
// and all of it after the holds were released.
func TestAdoptSignIn_LeavesIchorUIRequiringSignInWithAnAdmin(t *testing.T) {
	f := signInFlow(t)

	if err := f.runSignIn(); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	var u ichorv1alpha1.IchorUser
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); err != nil {
		t.Fatalf("no administrator was created: %v", err)
	}

	args := f.uiArgs(t)
	if !slices.Contains(args, "--users-enabled=true") || !slices.Contains(args, "--external-url="+defaultUIURL) ||
		slices.Contains(args, "--allow-unauthenticated=true") {
		t.Errorf("ichor-ui args = %v, want sign-in on at %s and no open flag", args, defaultUIURL)
	}

	if a := f.cluster(t).Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled || a.Ichor.URL != defaultUIURL {
		t.Errorf("spec.authentication = %+v, want the issuer %s", a, defaultUIURL)
	}

	// The zero-drift release first, then the administrator, then the authenticator.
	ev := f.events()
	release := slices.IndexFunc(ev, func(e string) bool { return strings.HasPrefix(e, "patch uuid-") })
	admin := slices.Index(ev, "create "+defaultAdminUser)
	authn := slices.Index(ev, "patch "+f.env.cluster.Name)

	if release < 0 || admin < release || authn < admin {
		t.Errorf("want release (%d) < administrator (%d) < authenticator (%d); events: %v", release, admin, authn, ev)
	}
}

// TestAdoptSignIn_ConsentNamesTheAPIServerRestartBeforeAnythingIsWritten: the prompt says what the
// authenticator costs, and declining it leaves the cluster exactly as it was.
func TestAdoptSignIn_ConsentNamesTheAPIServerRestartBeforeAnythingIsWritten(t *testing.T) {
	f := signInFlow(t)
	f.deps.AssumeYes = false
	f.deps.Stdin = strings.NewReader("n\n")

	err := f.runSignIn()
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("runAdopt = %v, want the abort", err)
	}

	// The plan already says it; the question that is answered must say it too.
	var prompt string

	for l := range strings.SplitSeq(f.env.stderr.String(), "\n") {
		if strings.Contains(l, "[y/N]") {
			prompt = l
		}
	}

	for _, want := range []string{"turn Ichor's sign-in on", "APIServerRestartRequired", "kube-apiserver restarts on each control plane", "ichor rollout config"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}

	if ev := f.events(); len(ev) != 0 || len(f.calls()) != 0 {
		t.Errorf("something was written before consent: %v", ev)
	}
}

// TestAdoptSignIn_TheGeneratedPasswordIsShownOnce: in the plain output and in the steps a person
// reads, the password appears once, and it is the last thing adopt prints before its next steps.
func TestAdoptSignIn_TheGeneratedPasswordIsShownOnce(t *testing.T) {
	for name, output := range map[string]string{"plain": outputYAML, "steps": outputHuman} {
		t.Run(name, func(t *testing.T) {
			f := signInFlow(t)
			f.deps.Output = output

			if err := f.runSignIn(); err != nil {
				t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
			}

			out := f.env.stderr.String()
			pw := generatedAdminPassword(t, out)

			if pw == "" || strings.Count(out, pw) != 1 {
				t.Fatalf("the password %q must appear exactly once:\n%s", pw, out)
			}

			if _, after, _ := strings.Cut(out, pw); strings.Contains(after, caRotationAdvice) || strings.Contains(after, authRolloutNotice) {
				t.Errorf("the password is buried above other output:\n%s", out)
			}
		})
	}
}

// TestAdoptSignIn_AllowUnauthenticatedStillSaysWhatOpenMeans: the flag is the way to an ichor-ui
// with no sign-in; it creates no administrator, and adopt warns in init's own words.
func TestAdoptSignIn_AllowUnauthenticatedStillSaysWhatOpenMeans(t *testing.T) {
	f := signInFlow(t)
	f.deps.AllowUnauthenticated = true

	if err := f.runSignIn(); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	if !strings.Contains(f.env.stderr.String(), openUIWarning) {
		t.Errorf("no open-mode warning:\n%s", f.env.stderr.String())
	}

	var u ichorv1alpha1.IchorUser
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: ichorControllerNamespace, Name: defaultAdminUser}, &u); err == nil {
		t.Error("an administrator was created although sign-in is off")
	}

	if args := f.uiArgs(t); slices.Contains(args, "--users-enabled=true") || !slices.Contains(args, "--allow-unauthenticated=true") {
		t.Errorf("ichor-ui args = %v, want it open and no users", args)
	}

	if a := f.cluster(t).Spec.Authentication; a != nil && a.Ichor != nil && a.Ichor.Enabled {
		t.Errorf("spec.authentication = %+v, want none", a)
	}
}

// TestAdoptSignIn_FlagsAreCheckedBeforeAnythingIsWritten: a bad issuer URL, or --ui-url with the
// open flag, stops the plan with nothing written.
func TestAdoptSignIn_FlagsAreCheckedBeforeAnythingIsWritten(t *testing.T) {
	for name, mutate := range map[string]func(*adoptDeps){
		"http url":              func(d *adoptDeps) { d.UIURL = strings.Replace(testUIURL, "https", "http", 1) },
		"ui-url with open flag": func(d *adoptDeps) { d.UIURL, d.AllowUnauthenticated = testUIURL, true },
		"admin with open flag":  func(d *adoptDeps) { d.AdminUser, d.AllowUnauthenticated = "root", true },
		"bad admin user name":   func(d *adoptDeps) { d.AdminUser = "Root User" },
	} {
		t.Run(name, func(t *testing.T) {
			f := signInFlow(t)
			mutate(&f.deps)

			err := f.runSignIn()
			if err == nil {
				t.Fatalf("runAdopt = %v, want a refusal", err)
			}

			if ev := f.events(); len(ev) != 0 {
				t.Errorf("something was written: %v", ev)
			}
		})
	}
}

// TestAdoptSignIn_RerunFinishesWhatStoppedAtTheAuthenticator: when the authenticator could not be
// set, the administrator exists and ichor-ui is at sign-in; running adopt again sets the authenticator
// and creates no second administrator.
func TestAdoptSignIn_RerunFinishesWhatStoppedAtTheAuthenticator(t *testing.T) {
	f := signInFlow(t)
	real := f.deps.K8s
	failing := true

	wc, ok := f.env.k8s.(client.WithWatch)
	if !ok {
		t.Fatal("the fake client does not watch")
	}

	f.deps.K8s = interceptor.NewClient(wc, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, o ...client.PatchOption) error {
			if _, isCluster := obj.(*ichorv1alpha1.TalosCluster); isCluster && failing {
				return apierrors.NewForbidden(schema.GroupResource{Resource: "talosclusters"}, obj.GetName(), nil)
			}

			return c.Patch(ctx, obj, p, o...)
		},
	})

	if err := f.runSignIn(); err == nil {
		t.Fatal("the first run succeeded although the authenticator could not be set")
	}

	if pw := generatedAdminPassword(t, f.env.stderr.String()); pw == "" {
		t.Fatal("the password was not shown although the administrator exists")
	}

	failing = false
	f.deps.K8s = real
	f.env.stderr.Reset()

	if err := f.runSignIn(); err != nil {
		t.Fatalf("second run: %v\n%s", err, f.env.stderr.String())
	}

	if strings.Contains(f.env.stderr.String(), "must change it at the first sign-in") {
		t.Errorf("the second run created another administrator:\n%s", f.env.stderr.String())
	}

	if a := f.cluster(t).Spec.Authentication; a == nil || a.Ichor == nil || !a.Ichor.Enabled {
		t.Errorf("spec.authentication = %+v after the rerun, want the issuer", a)
	}
}

// TestBuildAdoptPlan_AcceptsTheIssuerTheClusterItselfRenders: once adopt has turned sign-in on, the
// nodes carry Ichor's jwt entry; adopting them again (or adding a node back) is not a foreign
// authenticator.
func TestBuildAdoptPlan_AcceptsTheIssuerTheClusterItselfRenders(t *testing.T) {
	bundle := newTestBundle(t)
	cluster := newTestCluster()
	cluster.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{Ichor: &ichorv1alpha1.TalosClusterIchorIssuer{Enabled: true, URL: defaultUIURL}}

	machine := newTestMachine(cluster.Name)
	machine.Spec.Role = roleControlPlane

	certFile, _, _ := writeTestKeyPair(t)

	caPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}

	sys := &render.SystemInputs{OIDCTrust: &render.OIDCTrust{
		DiscoveryURL: "https://ichor-ui-service.ichor-system.svc:8444/.well-known/openid-configuration", CAPEM: string(caPEM),
	}}

	currentCfg, _, err := render.Render(cluster, machine, bundle, nil, sys)
	if err != nil {
		t.Fatalf("rendering the live configuration: %v", err)
	}

	if _, err := buildAdoptPlan(cluster, machine, bundle, nil, currentCfg, sys, ownership{}); err != nil {
		t.Fatalf("buildAdoptPlan refused the issuer the cluster itself renders: %v", err)
	}
}
