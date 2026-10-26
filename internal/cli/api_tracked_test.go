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

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const trackedArgoNS = "argocd"

// The fallback runs the same uiapi.Server, so it must also refuse to write a
// CR Argo CD tracks, not only the backend path.
func TestApproveFallbackRefusesArgoCDTrackedMachine(t *testing.T) {
	setAPITestFlags(t, true)

	m := strongMachine("tracked")
	m.Annotations = map[string]string{"argocd.argoproj.io/tracking-id": "app:ns/x"}
	argoCM := &corev1.ConfigMap{Name: "argocd-cm", Namespace: trackedArgoNS}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(m, argoCM).Build()

	c, err := connectAPI(context.Background(), apiOpts, apiDeps{K8s: k8s, Stderr: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatal(err)
	}

	if !c.Local {
		t.Fatal("want the local fallback")
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "tracked", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "Argo CD") {
		t.Fatalf("want FailedPrecondition naming Argo CD, got %v", err)
	}

	if getMachine(t, k8s, "tracked").Spec.IsApproved() {
		t.Fatal("the fallback wrote a tracked machine")
	}
}

// --argocd-namespace must reach the fallback's Server: with Argo CD in a
// namespace other than the default, the tracked machine is still refused.
func TestApproveFallbackHonoursArgoCDNamespaceFlag(t *testing.T) {
	setAPITestFlags(t, true)

	const argoNS = "gitops-argo"
	apiOpts.ArgoCDNamespace = argoNS

	m := strongMachine("tracked")
	m.Annotations = map[string]string{"argocd.argoproj.io/tracking-id": "app:ns/x"}
	argoCM := &corev1.ConfigMap{Name: "argocd-cm", Namespace: argoNS}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(m, argoCM).Build()

	c, err := connectAPI(context.Background(), apiOpts, apiDeps{K8s: k8s, Stderr: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatal(err)
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "tracked", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition with --argocd-namespace=%s, got %v", argoNS, err)
	}

	if getMachine(t, k8s, "tracked").Spec.IsApproved() {
		t.Fatal("the fallback wrote a tracked machine although --argocd-namespace named Argo CD's namespace")
	}
}

// --break-glass never goes through the backend (its ServiceAccount must not
// decide who may break glass) and reaches the Server as a break-glass
// request: with no repository to open the pull request in, a tracked
// resource is still refused, and the refusal says why.
const (
	trackingID  = "app:ns/x"
	trackingKey = "argocd.argoproj.io/tracking-id"
	argoCMName  = "argocd-cm"
)

func TestBreakGlassFlagUsesTheCallersCredentialsAndIsRefusedWithoutARepository(t *testing.T) {
	setAPITestFlags(t, true)

	apiOpts.BreakGlass = "node-2 is down"

	t.Cleanup(func() { apiOpts.BreakGlass = "" })

	m := strongMachine("tracked")
	m.Annotations = map[string]string{trackingKey: trackingID}
	argoCM := &corev1.ConfigMap{Name: argoCMName, Namespace: trackedArgoNS}
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(m, argoCM).Build()

	c, err := connectAPI(context.Background(), apiOpts, apiDeps{K8s: k8s, Stderr: &bytes.Buffer{}}, false)
	if err != nil {
		t.Fatal(err)
	}

	if !c.Local {
		t.Fatal("--break-glass must use the in-process server on the caller's credentials")
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "tracked", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "break-glass") {
		t.Fatalf("want a break-glass refusal, got %v", err)
	}

	if getMachine(t, k8s, "tracked").Spec.IsApproved() {
		t.Fatal("nothing may be written")
	}
}
