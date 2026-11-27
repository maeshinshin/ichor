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

// A complete install (its IchorInstallation exists) is left alone.
func TestInstallIchor_SkipsACompleteInstall(t *testing.T) {
	setVersion(t, testReleasedVersion)

	c := newWriteFake(t)
	seedDeployments(t, c, true)

	if err := c.Create(context.Background(), &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
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
