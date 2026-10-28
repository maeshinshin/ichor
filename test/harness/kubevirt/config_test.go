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

package main

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// TestAssertEndpoint proves the endpoint assertion actually fails loudly on a mismatch,
// rather than trivially agreeing with whatever it was just given.
func TestAssertEndpoint(t *testing.T) {
	input, err := generate.NewInput("test", "https://10.1.2.3:6443", constants.DefaultKubernetesVersion)
	if err != nil {
		t.Fatalf("NewInput: %v", err)
	}
	cfg, err := input.Config(machine.TypeControlPlane)
	if err != nil {
		t.Fatalf("Config: %v", err)
	}

	if err := assertEndpoint(cfg, "https://10.1.2.3:6443"); err != nil {
		t.Fatalf("assertEndpoint rejected the correct endpoint: %v", err)
	}

	err = assertEndpoint(cfg, "https://10.9.9.9:6443")
	if err == nil {
		t.Fatal("assertEndpoint accepted a mismatched endpoint; it must fail loudly instead")
	}
	if !strings.Contains(err.Error(), "10.1.2.3") || !strings.Contains(err.Error(), "10.9.9.9") {
		t.Fatalf("assertEndpoint's error does not name both the actual and wanted endpoint: %v", err)
	}
	t.Logf("assertEndpoint failed loudly as expected: %v", err)
}

// TestDemoMemory checks the demo VM memory knob: default kept, override lands in the manifest, junk refused.
func TestDemoMemory(t *testing.T) {
	const env = "ICHOR_UI_DEMO_TEST_MEMORY"

	got, err := demoMemory(env)
	if err != nil || got != "2560Mi" {
		t.Fatalf("unset: got %q, %v; want 2560Mi", got, err)
	}
	if m := demoVMManifest("x-cp", got); !strings.Contains(m, "guest: 2560Mi") {
		t.Fatalf("default manifest lost 2560Mi:\n%s", m)
	}

	t.Setenv(env, "3Gi")
	got, err = demoMemory(env)
	if err != nil || got != "3Gi" {
		t.Fatalf("override: got %q, %v; want 3Gi", got, err)
	}
	if m := demoVMManifest("x-cp", got); !strings.Contains(m, "guest: 3Gi") || strings.Contains(m, "2560Mi") {
		t.Fatalf("override manifest wrong:\n%s", m)
	}

	t.Setenv(env, "2560Mi\n  evil: 1")
	if _, err := demoMemory(env); err == nil {
		t.Fatal("accepted a malformed quantity")
	}
}
