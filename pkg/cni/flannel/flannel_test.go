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

package flannel

import (
	"context"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// TestPresetDoesNothing is the "flannel really does nothing" acceptance
// check: every method that can return empty output does, for both
// kube-proxy modes flannel is ever asked about.
func TestPresetDoesNothing(t *testing.T) {
	p := New()

	for _, mode := range []cni.KubeProxyMode{cni.KubeProxyEnabled, cni.KubeProxyReplaced} {
		if mode == cni.KubeProxyReplaced {
			continue // covered by TestTalosPatches_RejectsKubeProxyReplaced
		}

		patches, err := p.TalosPatches(mode)
		if err != nil {
			t.Fatalf("TalosPatches(%s): %v", mode, err)
		}
		if patches != nil {
			t.Fatalf("TalosPatches(%s) = %v, want nil", mode, patches)
		}

		manifest, err := p.BootstrapManifest(mode)
		if err != nil {
			t.Fatalf("BootstrapManifest(%s): %v", mode, err)
		}
		if manifest != nil {
			t.Fatalf("BootstrapManifest(%s) = %v, want nil", mode, manifest)
		}

		app, err := p.ArgoCDApplication(mode)
		if err != nil {
			t.Fatalf("ArgoCDApplication(%s): %v", mode, err)
		}
		if app != nil {
			t.Fatalf("ArgoCDApplication(%s) = %v, want nil", mode, app)
		}

		if err := p.CheckConsistency(context.Background(), cni.LiveConfig{}, mode); err != nil {
			t.Fatalf("CheckConsistency(%s): %v", mode, err)
		}
	}

	if p.SupportsKubeProxyReplacement() {
		t.Fatal("SupportsKubeProxyReplacement() = true, want false: flannel has no kube-proxy-free mode")
	}

	if workloads := p.HandoverWorkloads(); workloads != nil {
		t.Fatalf("HandoverWorkloads() = %v, want nil", workloads)
	}
}

func TestTalosPatches_RejectsKubeProxyReplaced(t *testing.T) {
	p := New()

	if _, err := p.TalosPatches(cni.KubeProxyReplaced); err == nil {
		t.Fatal("TalosPatches(replaced) = nil error, want an error: flannel cannot replace kube-proxy")
	}
}
