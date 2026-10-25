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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const handoffTestCluster = "handoff-cluster"

// TestRunGitopsHandoff_SetsAutomatic proves handoff's entire job: flip
// spec.cni.handover from "manual" to "automatic" and nothing else -- the
// actual hand-over (polling Argo CD, removing the inline manifest) is
// internal/controller.CNIHandoverReconciler's, not
// reimplemented here.
func TestRunGitopsHandoff_SetsAutomatic(t *testing.T) {
	cluster := newTestCluster()
	cluster.Name = handoffTestCluster
	cluster.Spec.CNI.Handover = "manual"

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()

	var out bytes.Buffer
	opts := gitopsHandoffOptions{Cluster: handoffTestCluster, Namespace: testNamespace}

	if err := runGitopsHandoff(context.Background(), opts, k8sClient, &out); err != nil {
		t.Fatalf("runGitopsHandoff: %v", err)
	}

	var got ichorv1alpha1.TalosCluster
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: handoffTestCluster}, &got); err != nil {
		t.Fatalf("fetching updated cluster: %v", err)
	}

	if got.Spec.CNI.Handover != cniHandoverAutomatic {
		t.Fatalf("spec.cni.handover = %q, want %q", got.Spec.CNI.Handover, cniHandoverAutomatic)
	}

	if !strings.Contains(out.String(), "manual") || !strings.Contains(out.String(), cniHandoverAutomatic) {
		t.Fatalf("expected output naming both the old and new value, got: %s", out.String())
	}
}

// TestRunGitopsHandoff_AlreadyAutomaticIsNoop proves a cluster already
// at "automatic" is reported, not re-written.
func TestRunGitopsHandoff_AlreadyAutomaticIsNoop(t *testing.T) {
	cluster := newTestCluster()
	cluster.Name = handoffTestCluster
	cluster.Spec.CNI.Handover = cniHandoverAutomatic

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()

	var out bytes.Buffer
	opts := gitopsHandoffOptions{Cluster: handoffTestCluster, Namespace: testNamespace}

	if err := runGitopsHandoff(context.Background(), opts, k8sClient, &out); err != nil {
		t.Fatalf("runGitopsHandoff: %v", err)
	}

	if !strings.Contains(out.String(), "already") {
		t.Fatalf("expected a no-op message, got: %s", out.String())
	}
}

// TestRunGitopsHandoff_ClusterNotFound proves a missing TalosCluster
// fails loudly.
func TestRunGitopsHandoff_ClusterNotFound(t *testing.T) {
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	opts := gitopsHandoffOptions{Cluster: handoffTestCluster, Namespace: testNamespace}
	if err := runGitopsHandoff(context.Background(), opts, k8sClient, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for a missing TalosCluster, got nil")
	}
}

// TestHandoffCmd_RejectsUnknownComponent proves the positional argument
// is validated against handoffComponents before any Kubernetes call is
// made.
func TestHandoffCmd_RejectsUnknownComponent(t *testing.T) {
	handoffCmd.SetArgs([]string{"not-a-real-component", "--cluster", handoffTestCluster})
	defer handoffCmd.SetArgs(nil)

	err := handoffCmd.RunE(handoffCmd, []string{"not-a-real-component"})
	if err == nil {
		t.Fatal("expected an error for an unknown component, got nil")
	}

	if !strings.Contains(err.Error(), "not-a-real-component") {
		t.Fatalf("expected the error to name the bad component, got: %v", err)
	}
}
