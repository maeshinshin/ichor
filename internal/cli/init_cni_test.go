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
)

const tunnelGeneve = "geneve"

func TestCNIPreset_Unknown(t *testing.T) {
	if _, err := cniPresetWithTunnel("calico", ""); err == nil {
		t.Fatal("expected an error for an unknown cni preset, got nil")
	}
}

// tunnelProtocolRe matches cilium-config's tunnel-protocol key whatever the
// chart's YAML quoting, followed by the value geneve.
var tunnelProtocolRe = regexp.MustCompile(`tunnel-protocol"?:\s*"?geneve`)

// TestCNIPreset_CiliumTunnel proves --cni-tunnel reaches both the bootstrap
// manifest, and that unset keeps the default.
func TestCNIPreset_CiliumTunnel(t *testing.T) {
	preset, err := cniPresetWithTunnel("cilium", tunnelGeneve)
	if err != nil {
		t.Fatalf("cniPresetWithTunnel(cilium, geneve): %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !tunnelProtocolRe.MatchString(out.InlineManifest.InlineManifestContents) || !strings.Contains(out.InlineManifest.InlineManifestContents, tunnelGeneve) {
		t.Error("bootstrap manifest does not carry the geneve tunnel")
	}

	if _, err := cniPresetWithTunnel("cilium", "gre"); err == nil {
		t.Error("cniPresetWithTunnel(cilium, gre) succeeded, want an error")
	}

	def, err := cniPresetWithTunnel("cilium", "")
	if err != nil {
		t.Fatalf("cniPresetWithTunnel(cilium, \"\"): %v", err)
	}

	defOut, err := buildCNIBootstrap(def, "my-cluster", testNamespace)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !strings.Contains(defOut.InlineManifest.InlineManifestContents, "vxlan") || tunnelProtocolRe.MatchString(defOut.InlineManifest.InlineManifestContents) {
		t.Error("unset tunnel did not render Cilium's default vxlan")
	}
}

// TestBuildCNIBootstrap_Cilium proves cilium contributes an inline manifest.
func TestBuildCNIBootstrap_Cilium(t *testing.T) {
	preset, err := cniPresetWithTunnel("cilium", "")
	if err != nil {
		t.Fatalf("cniPreset: %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if out.InlineManifest == nil || out.InlineManifest.InlineManifestContents == "" {
		t.Fatal("expected a non-empty inline manifest for cilium")
	}
}
