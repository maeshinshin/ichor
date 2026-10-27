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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildKokumiRepoCredsSecret(t *testing.T) {
	s := buildKokumiRepoCredsSecret("argocd")

	if s.Namespace != "argocd" || s.Name != kokumiRepoCredsSecretName {
		t.Fatalf("name/namespace = %s/%s", s.Namespace, s.Name)
	}

	if s.Labels[repositorySecretTypeLabel] != "repo-creds" {
		t.Fatalf("labels = %v, want %s=repo-creds", s.Labels, repositorySecretTypeLabel)
	}

	want := map[string]string{
		"type":                 "oci",
		"url":                  "oci://kokumi-registry.kokumi.svc.cluster.local:5000",
		"insecureOCIForceHttp": "true",
	}
	for k, v := range want {
		if s.StringData[k] != v {
			t.Fatalf("stringData[%s] = %q, want %q", k, s.StringData[k], v)
		}
	}
}

func TestValidateKokumiOptions_NeedsArgoCD(t *testing.T) {
	opts := validInitOptions()
	opts.Kokumi = true

	if err := validateKokumiOptions(opts); err == nil || !strings.Contains(err.Error(), "--kokumi") {
		t.Fatalf("validateKokumiOptions() = %v, want an error naming --kokumi", err)
	}

	opts.ArgoCD = true

	if err := validateKokumiOptions(opts); err != nil {
		t.Fatalf("with --argocd: %v", err)
	}
}

func runKokumiVariant(t *testing.T, kokumi bool) (string, client.Client, string) {
	t.Helper()

	cluster := newTestCluster()
	machine := newTestMachine(cluster.Name)
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	preset, err := cniPreset("cilium")
	if err != nil {
		t.Fatalf("cniPreset: %v", err)
	}

	boot, err := buildCNIBootstrap(preset, cluster.Name, cluster.Namespace, true)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	opts := validInitOptions()
	opts.ArgoCD = true
	opts.Kokumi = kokumi
	opts.ArgoCDNamespace = "argocd"
	opts.RepoURL = testRepoURL
	opts.GitOpsDir = t.TempDir()

	var buf bytes.Buffer

	if err := initArgoCD(context.Background(), opts, cluster, machine, nil, boot.ArgoCDApplication, c, &buf); err != nil {
		t.Fatalf("initArgoCD: %v", err)
	}

	return opts.GitOpsDir, c, buf.String()
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()

	out := map[string]string{}

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// The two variants render a byte-identical repository skeleton (Kokumi delivers nothing Ichor writes, so the Cilium
// Application stays a plain
// Argo CD Application carrying ignoreDifferences); only the Kokumi variant
// creates the OCI repo-creds Secret.
func TestInitArgoCD_KokumiVariantSkeletonAndSecret(t *testing.T) {
	aloneDir, aloneClient, aloneMsg := runKokumiVariant(t, false)
	kokDir, kokClient, kokMsg := runKokumiVariant(t, true)

	alone, kok := readTree(t, aloneDir), readTree(t, kokDir)
	if len(alone) == 0 || len(alone) != len(kok) {
		t.Fatalf("file sets differ: alone=%d kokumi=%d", len(alone), len(kok))
	}

	for name, body := range alone {
		if kok[name] != body {
			t.Fatalf("%s differs between variants", name)
		}
	}

	cilium := alone["apps/cni/application.yaml"]
	if !strings.Contains(cilium, "ignoreDifferences") || !strings.Contains(cilium, "CiliumIdentity") {
		t.Fatalf("Cilium Application lost ignoreDifferences:\n%s", cilium)
	}

	var secrets corev1.SecretList
	if err := aloneClient.List(context.Background(), &secrets); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("Argo CD alone must create no Secret here, got %d (%v)", len(secrets.Items), err)
	}

	var got corev1.Secret

	key := client.ObjectKey{Namespace: "argocd", Name: kokumiRepoCredsSecretName}
	if err := kokClient.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("Kokumi repo-creds Secret: %v", err)
	}

	if strings.Contains(aloneMsg, "--kokumi") || !strings.Contains(kokMsg, "did NOT install Kokumi") {
		t.Fatalf("disclaimer mismatch:\nalone=%s\nkokumi=%s", aloneMsg, kokMsg)
	}
}

func TestKokumiImages(t *testing.T) {
	imgs := kokumiImages()
	if len(imgs) != 2 || imgs[0] != "ghcr.io/kokumi-dev/kokumi:"+kokumiVersion {
		t.Fatalf("kokumiImages() = %v", imgs)
	}

	if !strings.Contains(imgs[1], "registry:3.1.2@sha256:") {
		t.Fatalf("registry image not digest-pinned: %v", imgs)
	}
}

func TestRunImagesList_KokumiFlag(t *testing.T) {
	var without, with bytes.Buffer

	if err := runImagesList(&without, imagesListOptions{TalosVersions: []string{"v1.11.0"}}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(without.String(), "kokumi") {
		t.Fatalf("Kokumi listed without --kokumi:\n%s", without.String())
	}

	if err := runImagesList(&with, imagesListOptions{TalosVersions: []string{"v1.11.0"}, Kokumi: true}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(with.String(), "ghcr.io/kokumi-dev/kokumi:"+kokumiVersion) {
		t.Fatalf("output lacks Kokumi's image:\n%s", with.String())
	}
}
