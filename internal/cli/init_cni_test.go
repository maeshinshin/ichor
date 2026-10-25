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

import "testing"

func TestCNIPreset_Unknown(t *testing.T) {
	if _, err := cniPreset("calico"); err == nil {
		t.Fatal("expected an error for an unknown cni preset, got nil")
	}
}

// TestBuildCNIBootstrap_Flannel proves architecture.md's "unless flannel":
// flannel contributes no inline manifest, no Talos patch and no Argo CD
// Application, even when --argocd is requested.
func TestBuildCNIBootstrap_Flannel(t *testing.T) {
	preset, err := cniPreset("flannel")
	if err != nil {
		t.Fatalf("cniPreset: %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, true)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if out.InlineManifest != nil {
		t.Fatalf("expected no inline manifest for flannel, got %+v", out.InlineManifest)
	}

	if out.Patch != nil {
		t.Fatalf("expected no Talos patch for flannel, got %+v", out.Patch)
	}

	if out.ArgoCDApplication != nil {
		t.Fatalf("expected no Argo CD Application for flannel, got %+v", out.ArgoCDApplication)
	}
}

// TestBuildCNIBootstrap_Cilium proves the opposite: cilium does contribute
// an inline manifest, and an Argo CD Application only when asked for one.
func TestBuildCNIBootstrap_Cilium(t *testing.T) {
	preset, err := cniPreset("cilium")
	if err != nil {
		t.Fatalf("cniPreset: %v", err)
	}

	withoutArgoCD, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, false)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if withoutArgoCD.InlineManifest == nil || withoutArgoCD.InlineManifest.InlineManifestContents == "" {
		t.Fatal("expected a non-empty inline manifest for cilium")
	}

	if withoutArgoCD.ArgoCDApplication != nil {
		t.Fatal("expected no Argo CD Application when wantArgoCD is false")
	}

	withArgoCD, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, true)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if withArgoCD.ArgoCDApplication == nil {
		t.Fatal("expected an Argo CD Application when wantArgoCD is true")
	}
}
