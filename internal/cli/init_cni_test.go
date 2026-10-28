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
	"regexp"
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const tunnelGeneve = "geneve"

// TestExportCNIApplication_CarriesTunnel proves export reproduces the tunnel
// init stored on the CR instead of falling back to Cilium's vxlan default.
func TestExportCNIApplication_CarriesTunnel(t *testing.T) {
	cluster := &ichorv1alpha1.TalosCluster{}
	cluster.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: "cilium", Tunnel: tunnelGeneve}

	app, err := exportCNIApplication(cluster)
	if err != nil {
		t.Fatalf("exportCNIApplication: %v", err)
	}

	if values := app.Spec.Source.Helm.Values; !strings.Contains(values, "tunnelProtocol: geneve") {
		t.Errorf("exported Argo CD values do not carry the geneve tunnel: %q", values)
	}
}

func TestCNIPreset_Unknown(t *testing.T) {
	if _, err := cniPresetWithTunnel("calico", ""); err == nil {
		t.Fatal("expected an error for an unknown cni preset, got nil")
	}
}

// tunnelProtocolRe matches cilium-config's tunnel-protocol key whatever the
// chart's YAML quoting, followed by the value geneve.
var tunnelProtocolRe = regexp.MustCompile(`tunnel-protocol"?:\s*"?geneve`)

// TestCNIPreset_CiliumTunnel proves --cni-tunnel reaches both the bootstrap
// manifest and the Argo CD Application values, and that unset keeps the default.
func TestCNIPreset_CiliumTunnel(t *testing.T) {
	preset, err := cniPresetWithTunnel("cilium", tunnelGeneve)
	if err != nil {
		t.Fatalf("cniPresetWithTunnel(cilium, geneve): %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, true)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !tunnelProtocolRe.MatchString(out.InlineManifest.InlineManifestContents) || !strings.Contains(out.InlineManifest.InlineManifestContents, tunnelGeneve) {
		t.Error("bootstrap manifest does not carry the geneve tunnel")
	}

	if values := out.ArgoCDApplication.Spec.Source.Helm.Values; !strings.Contains(values, tunnelGeneve) {
		t.Errorf("Argo CD values do not carry the geneve tunnel: %q", values)
	}

	if _, err := cniPresetWithTunnel("cilium", "gre"); err == nil {
		t.Error("cniPresetWithTunnel(cilium, gre) succeeded, want an error")
	}

	def, err := cniPresetWithTunnel("cilium", "")
	if err != nil {
		t.Fatalf("cniPresetWithTunnel(cilium, \"\"): %v", err)
	}

	defOut, err := buildCNIBootstrap(def, "my-cluster", testNamespace, false)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !strings.Contains(defOut.InlineManifest.InlineManifestContents, "vxlan") || tunnelProtocolRe.MatchString(defOut.InlineManifest.InlineManifestContents) {
		t.Error("unset tunnel did not render Cilium's default vxlan")
	}
}

// TestBuildCNIBootstrap_Cilium proves cilium contributes
// an inline manifest, and an Argo CD Application only when asked for one.
func TestBuildCNIBootstrap_Cilium(t *testing.T) {
	preset, err := cniPresetWithTunnel("cilium", "")
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

	// Without syncPolicy.automated Argo CD never syncs it and the
	// hand-over never completes.
	sp := withArgoCD.ArgoCDApplication.Spec.SyncPolicy
	if sp == nil || sp.Automated == nil || !sp.Automated.Prune || !sp.Automated.SelfHeal {
		t.Fatalf("cilium syncPolicy = %+v, want automated {prune: true, selfHeal: true}", sp)
	}

	root, err := buildRootApplication(testRepoURL, "clusters/c")
	if err != nil {
		t.Fatal(err)
	}

	sp = root.Spec.SyncPolicy
	if sp == nil || sp.Automated == nil || !sp.Automated.Prune || !sp.Automated.SelfHeal {
		t.Fatalf("root syncPolicy = %+v, want automated {prune: true, selfHeal: true}", sp)
	}
}
