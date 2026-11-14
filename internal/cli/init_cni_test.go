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

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

const tunnelGeneve = "geneve"

// tunnelProtocolRe matches cilium-config's tunnel-protocol key whatever the
// chart's YAML quoting, followed by the value geneve.
var tunnelProtocolRe = regexp.MustCompile(`tunnel-protocol"?:\s*"?geneve`)

// TestCNIPreset_CiliumTunnel proves --cni-tunnel reaches both the bootstrap
// manifest, and that unset keeps the default.
func TestCNIPreset_CiliumTunnel(t *testing.T) {
	preset, err := cilium.New().WithTunnel(tunnelGeneve)
	if err != nil {
		t.Fatalf("WithTunnel(geneve): %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !tunnelProtocolRe.MatchString(out.InlineManifest.InlineManifestContents) || !strings.Contains(out.InlineManifest.InlineManifestContents, tunnelGeneve) {
		t.Error("bootstrap manifest does not carry the geneve tunnel")
	}

	if _, err := cilium.New().WithTunnel("gre"); err == nil {
		t.Error("WithTunnel(gre) succeeded, want an error")
	}

	def, err := cilium.New().WithTunnel("")
	if err != nil {
		t.Fatalf("WithTunnel(\"\"): %v", err)
	}

	defOut, err := buildCNIBootstrap(def, "my-cluster", testNamespace, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if !strings.Contains(defOut.InlineManifest.InlineManifestContents, "vxlan") || tunnelProtocolRe.MatchString(defOut.InlineManifest.InlineManifestContents) {
		t.Error("unset tunnel did not render Cilium's default vxlan")
	}
}

// TestBuildCNIBootstrap_Cilium proves cilium contributes an inline manifest.
func TestBuildCNIBootstrap_Cilium(t *testing.T) {
	preset, err := cilium.New().WithTunnel("")
	if err != nil {
		t.Fatalf("cniPreset: %v", err)
	}

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if out.InlineManifest == nil || out.InlineManifest.InlineManifestContents == "" {
		t.Fatal("expected a non-empty inline manifest for cilium")
	}
}

// ciliumOperatorReplicas is spec.replicas of the cilium-operator Deployment in a rendered manifest.
func ciliumOperatorReplicas(t *testing.T, manifest string) int {
	t.Helper()

	for doc := range strings.SplitSeq(manifest, "\n---\n") {
		var obj struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
		}

		if err := sigsyaml.Unmarshal([]byte(doc), &obj); err != nil {
			continue
		}

		if obj.Kind == "Deployment" && obj.Metadata.Name == "cilium-operator" {
			return obj.Spec.Replicas
		}
	}

	t.Fatal("no cilium-operator Deployment in the manifest")

	return 0
}

// `ichor init` renders Cilium into the first control plane's machine config when that node is the only
// Node there is, so an HA cluster's operator starts with one replica, not the desired count's two.
func TestBuildCNIBootstrap_HAOperatorStartsWithOneReplica(t *testing.T) {
	preset := cilium.New().WithControlPlanes(3)

	out, err := buildCNIBootstrap(preset, "my-cluster", testNamespace, cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("buildCNIBootstrap: %v", err)
	}

	if got := ciliumOperatorReplicas(t, out.InlineManifest.InlineManifestContents); got != 1 {
		t.Errorf("operator replicas = %d, want 1 for the one node init bootstraps", got)
	}
}
