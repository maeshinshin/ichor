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
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const partialWebhookName = "ichor-validating-webhook-configuration"

// partialInstallManifest is signInManifest plus the webhook configuration the install applies last.
var partialInstallManifest = signInManifest + `---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: ` + partialWebhookName + `
`

// seedDeployments seeds the Deployments of an install that stopped before its policies and webhooks.
func seedDeployments(t *testing.T, c client.Client, withUI bool) {
	t.Helper()

	for _, o := range uiObjects() {
		d, ok := o.(*appsv1.Deployment)
		if !ok || (!withUI && d.Labels[uiComponentLabel] == uiComponentValue) {
			continue
		}

		if err := c.Create(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
}

func partialWriter(c client.Client, out *bytes.Buffer) *adoptWriter {
	return &adoptWriter{c: c, res: &adoptWriteResult{}, in: adoptWriteInput{
		Stderr: out, Interval: time.Millisecond, WebhookWait: time.Second,
		Ichor: &adoptWriteIchor{Namespace: ichorControllerNamespace, Manifest: []byte(partialInstallManifest)},
	}}
}

// An install that stopped after its Deployments is finished by the next adopt: the webhooks and the
// IchorInstallation are made, and the message says an earlier install is being completed.
func TestInstallIchor_CompletesAPartialInstall(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedDeployments(t, c, true)

	var out bytes.Buffer

	w := partialWriter(c, &out)
	if err := w.installIchor(context.Background()); err != nil {
		t.Fatalf("installIchor: %v\n%s", err, out.String())
	}

	vwc := &unstructured.Unstructured{}
	vwc.SetAPIVersion("admissionregistration.k8s.io/v1")
	vwc.SetKind("ValidatingWebhookConfiguration")

	if err := c.Get(context.Background(), types.NamespacedName{Name: partialWebhookName}, vwc); err != nil {
		t.Errorf("the webhook configuration was not applied: %v", err)
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Errorf("the IchorInstallation was not created: %v", err)
	}

	if !strings.Contains(out.String(), "completing an earlier, partial install") {
		t.Errorf("no message says adopt completes an earlier install:\n%s", out.String())
	}
}

// The controller seeds the IchorInstallation (no pin) as soon as it runs, so an install interrupted after
// its Deployments still has one: the next adopt applies what was left out and pins what the seed lacks.
func TestInstallIchor_FinishesAnInstallTheControllerSeeded(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedDeployments(t, c, true)

	if err := c.Create(context.Background(), &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:    testReleasedVersion,
			Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerInstaller},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	w := partialWriter(c, &out)
	w.in.Ichor.Pin = installerPin{Digest: "sha256:" + strings.Repeat("a", 64), SkipSignature: true}

	if err := w.installIchor(context.Background()); err != nil {
		t.Fatalf("installIchor: %v\n%s", err, out.String())
	}

	for _, o := range []struct{ kind, apiVersion, name string }{
		{"ValidatingWebhookConfiguration", "admissionregistration.k8s.io/v1", partialWebhookName},
	} {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(o.apiVersion)
		u.SetKind(o.kind)

		if err := c.Get(context.Background(), types.NamespacedName{Name: o.name}, u); err != nil {
			t.Errorf("%s %s was not applied: %v", o.kind, o.name, err)
		}
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Fatal(err)
	}

	if inst.Spec.InstallerDigest != w.in.Ichor.Pin.Digest || inst.Spec.InstallerSignature == nil ||
		inst.Spec.InstallerSignature.Mode != ichorv1alpha1.InstallerSignatureSkip {
		t.Errorf("the pin was not set on the seeded installation: %+v", inst.Spec)
	}
}

// A Helm-managed install is left alone: nothing is applied and no IchorInstallation is made.
func TestInstallIchor_SkipsAHelmManagedInstall(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedDeployments(t, c, true)

	if err := c.Create(context.Background(), &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:    testReleasedVersion,
			Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerHelm},
		},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	w := partialWriter(c, &out)
	if err := w.installIchor(context.Background()); err != nil {
		t.Fatalf("installIchor: %v", err)
	}

	if !slices.Equal(w.res.Unchanged, []string{"Ichor installation"}) || len(w.res.Created) != 0 {
		t.Errorf("created %v, unchanged %v; want the install Unchanged", w.res.Created, w.res.Unchanged)
	}

	vwc := &unstructured.Unstructured{}
	vwc.SetAPIVersion("admissionregistration.k8s.io/v1")
	vwc.SetKind("ValidatingWebhookConfiguration")

	if err := c.Get(context.Background(), types.NamespacedName{Name: partialWebhookName}, vwc); err == nil {
		t.Error("the manifest was applied to a Helm-managed install")
	}
}

// An install that stopped before ichor-ui has no ichor-ui to read: the plan treats it as a first adopt.
func TestAdoptSignIn_PartialInstallWithoutUIDoesNotFailThePlan(t *testing.T) {
	f := signInFlow(t)
	seedDeployments(t, f.env.k8s, false)

	err := f.runSignIn()
	if err != nil && strings.Contains(err.Error(), "no Deployment labelled") {
		t.Fatalf("runAdopt: %v", err)
	}
}

// seedRunningIchor seeds a non-Helm Ichor that runs at running: its IchorInstallation with the version
// the controller reports and the controller Deployment.
func seedRunningIchor(t *testing.T, c client.Client, running string) {
	t.Helper()

	ctx := context.Background()

	if err := c.Create(ctx, &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:    running,
			Deployment: &ichorv1alpha1.IchorDeploymentSpec{Manager: ichorv1alpha1.IchorManagerInstaller},
		},
		Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: running},
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Create(ctx, &appsv1.Deployment{
		Name: controllerDeploymentName, Namespace: ichorControllerNamespace,
		Labels: map[string]string{controllerLabel: controllerLabelValue},
	}); err != nil {
		t.Fatal(err)
	}
}

func webhookApplied(t *testing.T, c client.Client) bool {
	t.Helper()

	vwc := &unstructured.Unstructured{}
	vwc.SetAPIVersion("admissionregistration.k8s.io/v1")
	vwc.SetKind("ValidatingWebhookConfiguration")

	return c.Get(context.Background(), types.NamespacedName{Name: partialWebhookName}, vwc) == nil
}

// A running Ichor newer than the CLI is not overwritten with the CLI's older CRDs and webhooks, by the
// early check or by the write step.
func TestInstallIchor_RefusesARunningIchorNewerThanTheCLI(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedRunningIchor(t, c, "v0.3.7")

	var out bytes.Buffer

	err := partialWriter(c, &out).installIchor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "v0.3.7") || !strings.Contains(err.Error(), testReleasedVersion) {
		t.Fatalf("installIchor = %v; want a refusal naming v0.3.7 and %s", err, testReleasedVersion)
	}

	if webhookApplied(t, c) {
		t.Error("the install manifest was applied over a newer Ichor")
	}

	if err := checkIchorPresence(context.Background(), c, &out, testReleasedVersion, false); err == nil {
		t.Error("checkIchorPresence let adopt start against a newer running Ichor")
	}
}

// adopt does not upgrade a running Ichor either: that is `ichor upgrade ichor`.
func TestInstallIchor_RefusesARunningIchorOlderThanTheCLI(t *testing.T) {
	setVersion(t, "v0.3.7")

	c := newWriteFake(t)
	seedRunningIchor(t, c, testReleasedVersion)

	err := partialWriter(c, &bytes.Buffer{}).installIchor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ichor upgrade ichor") {
		t.Fatalf("installIchor = %v; want a refusal pointing at ichor upgrade ichor", err)
	}

	if webhookApplied(t, c) {
		t.Error("the install manifest was applied over an older running Ichor")
	}
}

func TestInstallIchor_AppliesToARunningIchorOfTheSameVersion(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedRunningIchor(t, c, testReleasedVersion)

	if err := partialWriter(c, &bytes.Buffer{}).installIchor(context.Background()); err != nil {
		t.Fatalf("installIchor: %v", err)
	}

	if !webhookApplied(t, c) {
		t.Error("the install manifest was not applied to an Ichor of the same version")
	}
}

// With no IchorInstallation the install is partial, whatever version the CLI is: it is finished.
func TestInstallIchor_AppliesToAPartialInstallOfAnyVersion(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedDeployments(t, c, true)

	if err := partialWriter(c, &bytes.Buffer{}).installIchor(context.Background()); err != nil || !webhookApplied(t, c) {
		t.Fatalf("installIchor = %v, applied %v; want the partial install finished", err, webhookApplied(t, c))
	}
}
