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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildRepoSecret_NoCredentialIsNilNotEmpty(t *testing.T) {
	secret, err := buildRepoSecret("argocd", "https://example.invalid/repo.git", "", "")
	if err != nil {
		t.Fatalf("buildRepoSecret: %v", err)
	}

	if secret != nil {
		t.Fatalf("expected nil for a public repo with no credential, got %+v", secret)
	}
}

func TestBuildRepoSecret_WithCredential(t *testing.T) {
	secret, err := buildRepoSecret("argocd", "https://example.invalid/repo.git", "alice", "token123")
	if err != nil {
		t.Fatalf("buildRepoSecret: %v", err)
	}

	if secret == nil {
		t.Fatal("expected a Secret, got nil")
	}

	if secret.Namespace != "argocd" {
		t.Fatalf("namespace = %q, want argocd", secret.Namespace)
	}

	if secret.Labels[repositorySecretTypeLabel] != repositorySecretTypeValue {
		t.Fatalf("labels = %v, want %s=%s", secret.Labels, repositorySecretTypeLabel, repositorySecretTypeValue)
	}

	if secret.StringData["username"] != "alice" || secret.StringData["password"] != "token123" {
		t.Fatalf("stringData = %v, want username/password set", secret.StringData)
	}
}

// TestInitArgoCD_RepoURLWithEmbeddedCredentialRejected proves the second
// of the two guarantees pkg/gitops provides (the coordinator's brief: "it
// checks in two places that RepoURL carries no userinfo, and refuses to
// write a *corev1.Secret"): a --repo value with embedded userinfo is
// rejected before any Secret or skeleton file is created.
func TestInitArgoCD_RepoURLWithEmbeddedCredentialRejected(t *testing.T) {
	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)

	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	opts := validInitOptions()
	opts.ArgoCD = true
	opts.RepoURL = "https://user:pass@example.invalid/repo.git"
	opts.GitOpsDir = t.TempDir()

	var stderr bytes.Buffer

	err := initArgoCD(context.Background(), opts, cluster, machine, nil, nil, k8sClient, &stderr)
	if err == nil {
		t.Fatal("expected an error for a repoURL with embedded credentials, got nil")
	}

	var secrets corev1.SecretList
	if listErr := k8sClient.List(context.Background(), &secrets); listErr != nil {
		t.Fatalf("listing secrets: %v", listErr)
	}

	if len(secrets.Items) != 0 {
		t.Fatalf("expected no Secret to be created, got %+v", secrets.Items)
	}
}

// TestSkeletonWrite_RefusesSecret proves buildSkeleton never gives
// pkg/gitops.Skeleton.Write anything it would have to refuse -- the CRs
// map in this command only ever carries TalosCluster/TalosMachine/
// TalosConfigPatch values, never a Secret.
func TestSkeletonWrite_RefusesSecret(t *testing.T) {
	cluster := newTestCluster()

	skeleton := buildSkeleton(cluster, nil, nil, map[string]any{"oops.yaml": &corev1.Secret{}})

	if err := skeleton.Write(t.TempDir()); err == nil {
		t.Fatal("expected pkg/gitops.WriteObject to refuse a Secret, got nil error")
	}
}
