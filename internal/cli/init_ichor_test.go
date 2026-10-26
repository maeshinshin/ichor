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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/installmanifest"
	"github.com/maeshinshin/ichor/internal/version"
)

func TestApplyIchorManifest_RefusesPlaceholder(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	err := applyIchorManifest(context.Background(), k8sClient, []byte(installmanifest.PlaceholderSentinel+"\n# nothing else"), &warn)
	if err == nil {
		t.Fatal("expected an error for the placeholder manifest, got nil")
	}
}

func TestApplyIchorManifest_AppliesEveryDocument(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("applyIchorManifest: %v", err)
	}

	var ns corev1.Namespace
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: ichorControllerNamespace}, &ns); err != nil {
		t.Fatalf("getting applied Namespace: %v", err)
	}

	var sa corev1.ServiceAccount
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ichorControllerNamespace, Name: "ichor-controller"}, &sa); err != nil {
		t.Fatalf("getting applied ServiceAccount: %v", err)
	}
}

// TestApplyIchorManifest_AlreadyExistsIsNotAnError proves a re-run
// against a cluster that already has some of the manifest applied
// doesn't fail just because of that.
func TestApplyIchorManifest_AlreadyExistsIsNotAnError(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	var warn bytes.Buffer

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	if err := applyIchorManifest(context.Background(), k8sClient, []byte(testIchorManifest), &warn); err != nil {
		t.Fatalf("second apply: %v", err)
	}
}

func newIchorInstallationFake(t *testing.T) client.Client {
	t.Helper()

	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func TestCreateIchorInstallation_RecordsTheBinarysVersion(t *testing.T) {
	old := version.Version
	version.Version = "v0.3.1"

	t.Cleanup(func() { version.Version = old })

	c := newIchorInstallationFake(t)

	var warn bytes.Buffer
	if err := createIchorInstallation(context.Background(), c, &warn); err != nil {
		t.Fatalf("createIchorInstallation: %v", err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := c.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
		t.Fatalf("the singleton was not created: %v", err)
	}

	if got.Spec.Version != "v0.3.1" || got.Spec.ManagementMode != ichorv1alpha1.IchorManagementModeIchor {
		t.Errorf("spec = %+v, want version v0.3.1 in ichor mode", got.Spec)
	}
}

func TestCreateIchorInstallation_SkipsADevBuild(t *testing.T) {
	old := version.Version
	version.Version = "dev"

	t.Cleanup(func() { version.Version = old })

	c := newIchorInstallationFake(t)

	var warn bytes.Buffer
	if err := createIchorInstallation(context.Background(), c, &warn); err != nil {
		t.Fatalf("createIchorInstallation: %v", err)
	}

	var list ichorv1alpha1.IchorInstallationList
	if err := c.List(context.Background(), &list); err != nil || len(list.Items) != 0 {
		t.Errorf("a dev build created %d IchorInstallations (err %v)", len(list.Items), err)
	}

	if !strings.Contains(warn.String(), "not a released version") {
		t.Errorf("no notice was printed: %q", warn.String())
	}
}
