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
// rather than trivially agreeing with whatever it was just given (ADR 0007).
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
	if m := demoVMManifest("x-cp", "x", got, httpISO()); !strings.Contains(m, "guest: 2560Mi") {
		t.Fatalf("default manifest lost 2560Mi:\n%s", m)
	}

	t.Setenv(env, "3Gi")
	got, err = demoMemory(env)
	if err != nil || got != "3Gi" {
		t.Fatalf("override: got %q, %v; want 3Gi", got, err)
	}
	m := demoVMManifest("x-cp", "x", got, httpISO())
	if !strings.Contains(m, "guest: 3Gi") || strings.Contains(m, "2560Mi") {
		t.Fatalf("override manifest wrong:\n%s", m)
	}

	t.Setenv(env, "2560Mi\n  evil: 1")
	if _, err := demoMemory(env); err == nil {
		t.Fatal("accepted a malformed quantity")
	}
}

// TestBaseCacheEnabled: on by default, off only for the explicit off values.
func TestBaseCacheEnabled(t *testing.T) {
	t.Setenv(baseCacheEnv, "")
	if !baseCacheEnabled() {
		t.Fatal("cache must default to on")
	}
	for _, v := range []string{"1", "true", "yes"} {
		t.Setenv(baseCacheEnv, v)
		if !baseCacheEnabled() {
			t.Fatalf("%q turned the cache off", v)
		}
	}
	for _, v := range []string{"0", "false", "OFF", "no"} {
		t.Setenv(baseCacheEnv, v)
		if baseCacheEnabled() {
			t.Fatalf("%q left the cache on", v)
		}
	}
}

// TestBaseCacheName: DNS-safe, per Talos version and schematic.
func TestBaseCacheName(t *testing.T) {
	got := baseCacheName()
	want := "ichor-e2e-talos-v1-14-3-" + talosFactorySchematic[:8] + "-base"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, ".") {
		t.Fatalf("%q contains a dot", got)
	}
}

// TestVMManifestISOSource: the old path keeps the HTTP import; the cache path clones the PVC at the cache's size.
func TestVMManifestISOSource(t *testing.T) {
	for name, tmpl := range map[string]string{"single": vmManifestTmpl, "ha": haVMManifestTmpl} {
		old := renderVM(tmpl, "x-0", httpISO())
		if !strings.Contains(old, "metal-amd64.iso") || strings.Contains(old, "pvc:") ||
			!strings.Contains(old, "storage: 1Gi") {
			t.Fatalf("%s: old path wrong:\n%s", name, old)
		}
		cached := renderVM(tmpl, "x-0", clonedISO(baseCacheName(), "2Gi"))
		for _, want := range []string{"pvc:", "name: " + baseCacheName(), "namespace: ichor-e2e", "storage: 2Gi"} {
			if !strings.Contains(cached, want) {
				t.Fatalf("%s: cached manifest lacks %q:\n%s", name, want, cached)
			}
		}
		if strings.Contains(cached, "http:") || strings.Contains(cached, "%!") {
			t.Fatalf("%s: cached manifest still imports or is malformed:\n%s", name, cached)
		}
		if !strings.Contains(cached, "blank: {}") {
			t.Fatalf("%s: the system disk must stay blank so Ichor installs:\n%s", name, cached)
		}
	}
}
