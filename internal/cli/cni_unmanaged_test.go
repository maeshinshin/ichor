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

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func unmanagedTestClients(t *testing.T) *apiClients {
	t.Helper()

	setAPITestFlags(t, true)

	cl := newTestCluster()
	cl.Spec.CNI.Preset = ichorv1alpha1.TalosClusterCNIPresetUnmanaged
	cl.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{Detected: "flannel"}

	return localClients(apiOpts, apiDeps{K8s: fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cl).Build()})
}

func TestClusterStatusPrintsTheUnmanagedCNILine(t *testing.T) {
	var out bytes.Buffer

	if err := runClusterStatus(context.Background(), unmanagedTestClients(t), &out, "adopt-cluster"); err != nil {
		t.Fatal(err)
	}

	if want := "cni: unmanaged (flannel, not managed by Ichor)\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("want %q in:\n%s", want, out.String())
	}
}

func TestClusterStatusPrintsCiliumForAManagedCluster(t *testing.T) {
	c, _ := cniTestClient(t, true)

	var out bytes.Buffer
	if err := runClusterStatus(context.Background(), c, &out, "adopt-cluster"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "cni: cilium\n") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestCniCommandsRefuseAnUnmanagedCluster(t *testing.T) {
	err := refuseUnmanagedCNI(context.Background(), unmanagedTestClients(t), "")
	if err == nil || err.Error() != ichorv1alpha1.CNIUnmanagedMessage {
		t.Fatalf("want the unmanaged message, got %v", err)
	}
}

func TestCniCommandsPassForAManagedCluster(t *testing.T) {
	c, _ := cniTestClient(t, true)

	if err := refuseUnmanagedCNI(context.Background(), c, "adopt-cluster"); err != nil {
		t.Fatal(err)
	}
}
