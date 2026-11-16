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

package installer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// storedTalosStorage is a TalosStorage as the retired kind stores it, in namespace ns.
func storedTalosStorage(ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: ichorAPIVersion,
		kKind:       "TalosStorage",
		kMetadata:   map[string]any{kName: "leftover", kNamespace: ns},
		kSpec: map[string]any{
			"clusterRef": map[string]any{kName: "cluster"},
			"nodes":      map[string]any{},
			"diskPreset": "rook-v1",
		},
	}}
}

// listRefusedClient refuses every list of TalosStorage objects, as an installer role that lacks
// the list rule does. Every other call goes to the real API server.
type listRefusedClient struct{ client.Client }

func (c listRefusedClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if list.GetObjectKind().GroupVersionKind().Kind == "TalosStorageList" {
		return apierrors.NewForbidden(schema.GroupResource{Group: "ichor.maesh.dev", Resource: "talosstorages"}, "",
			errors.New("installer role lacks list on talosstorages"))
	}

	return c.Client.List(ctx, list, opts...)
}

// The installer refuses to install while a TalosStorage object remains: this release does not read
// the kind, so the node configuration of the object's nodes would silently lose its volume document.
//
func TestInstallerRefusesWhileATalosStorageObjectRemains(t *testing.T) {
	f := newFixture(t)

	installOldCRD(t, loadCRD(t, "testdata/talosstorages-before-removal.yaml"))
	createWhenEstablished(t, storedTalosStorage(f.ns))

	err := Run(context.Background(), k8sClient, Options{
		Namespace: f.ns, RunID: f.run, Manifest: f.manifest("ctrl:v1", ""), Ready: noWait(new([]string)),
	})
	if err == nil || !strings.Contains(err.Error(), "1 TalosStorage object(s) remain") {
		t.Fatalf("Run error = %v, want a refusal that counts the TalosStorage objects that remain", err)
	}

	if !strings.Contains(err.Error(), "TalosStoragePreset") || !strings.Contains(err.Error(), "kubectl delete crd talosstorages.ichor.maesh.dev") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}

	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("the refusal did not roll back the RBAC it applied first: %v", err)
	}
}

// The retired CRD with no objects left must not block the install: a refusal on an empty kind
// would be worse than the defect it guards against.
func TestInstallerAllowsAnEmptyRetiredCRD(t *testing.T) {
	f := newFixture(t)

	installOldCRD(t, loadCRD(t, "testdata/talosstorages-before-removal.yaml"))
	waitEstablished(t, talosStoragesCRD)

	runInstaller(t, f, f.run, f.manifest("ctrl:v1", ""))

	if got := f.image(t, "ichor-controller-manager"); got != "ctrl:v1" {
		t.Errorf("controller image = %q, want ctrl:v1: an empty retired CRD must not stop the install", got)
	}
}

// A list that fails is not an empty list. The installer refuses, and the message says it could not
// read the objects, so the operator fixes the rights instead of recreating objects that may not exist.
func TestInstallerRefusesWhenTheTalosStorageListFails(t *testing.T) {
	f := newFixture(t)

	installOldCRD(t, loadCRD(t, "testdata/talosstorages-before-removal.yaml"))

	err := Run(context.Background(), listRefusedClient{k8sClient}, Options{
		Namespace: f.ns, RunID: f.run, Manifest: f.manifest("ctrl:v1", ""), Ready: noWait(new([]string)),
	})
	if err == nil || !strings.Contains(err.Error(), "cannot list TalosStorage objects") {
		t.Fatalf("Run error = %v, want a refusal that says the list could not be read", err)
	}

	if strings.Contains(err.Error(), "remain") {
		t.Errorf("a failed list is reported as objects remaining, which is not what happened: %v", err)
	}

	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("the refusal did not roll back the RBAC it applied first: %v", err)
	}
}

// waitEstablished waits until the CRD is served. A listed kind is served only once it is Established,
// and an installed CRD in a live cluster always is.
func waitEstablished(t *testing.T, name string) {
	t.Helper()

	for range 100 {
		crd := &unstructured.Unstructured{}
		crd.SetAPIVersion("apiextensions.k8s.io/v1")
		crd.SetKind(kindCRD)

		if err := k8sClient.Get(context.Background(), client.ObjectKey{Name: name}, crd); err == nil && crdEstablished(crd) {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("the CRD %s was never established", name)
}
