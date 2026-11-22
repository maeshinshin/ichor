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

	"github.com/maeshinshin/ichor/test/internal/kube"
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
	disks := demoDisks{system: defaultDemoSystemDisk}
	if m := demoVMManifest("x-cp", "x", got, httpISO(), disks); !strings.Contains(m, "guest: 2560Mi") {
		t.Fatalf("default manifest lost 2560Mi:\n%s", m)
	}

	t.Setenv(env, "3Gi")
	got, err = demoMemory(env)
	if err != nil || got != "3Gi" {
		t.Fatalf("override: got %q, %v; want 3Gi", got, err)
	}
	m := demoVMManifest("x-cp", "x", got, httpISO(), disks)
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
	want := "ichor-e2e-talos-v1-14-5-" + talosFactorySchematic[:8] + "-base"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if strings.Contains(got, ".") {
		t.Fatalf("%q contains a dot", got)
	}
}

// TestVMManifestISOSource: the old path keeps the HTTP import; the cache path clones the PVC at the cache's size.
func TestVMManifestISOSource(t *testing.T) {
	for _, name := range []string{"kubevirt", "kubevirt-ha"} {
		old := renderVM("x-0", name, httpISO())
		if !strings.Contains(old, "metal-amd64.iso") || strings.Contains(old, "pvc:") ||
			!strings.Contains(old, "storage: 1Gi") {
			t.Fatalf("%s: old path wrong:\n%s", name, old)
		}
		cached := renderVM("x-0", name, clonedISO(baseCacheName(), "2Gi"))
		for _, want := range []string{"pvc:", "name: " + baseCacheName(), "namespace: __NAMESPACE__", "storage: 2Gi",
			"cdi.kubevirt.io/storage.bind.immediate.requested", "storageClassName: " + kube.StorageClass()} {
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

// TestDemoDisks: spare disks become N emptyDisk volumes of the given size on a worker, the system disk
// follows its parameter, and the defaults leave the manifest as it was.
func TestDemoDisks(t *testing.T) {
	d, err := demoDiskConfig()
	if err != nil || d.system != "10Gi" || d.spare != 0 {
		t.Fatalf("defaults: %+v, %v", d, err)
	}
	m := demoVMManifest("x-w", "x", "2560Mi", httpISO(), d)
	if strings.Contains(m, "emptyDisk") || !strings.Contains(m, "storage: 10Gi") {
		t.Fatalf("defaults changed the manifest:\n%s", m)
	}

	t.Setenv(demoSpareDisksEnv, "3")
	t.Setenv(demoSpareSizeEnv, "20Gi")
	t.Setenv(demoSystemDiskEnv, "40Gi")
	d, err = demoDiskConfig()
	if err != nil {
		t.Fatal(err)
	}
	m = demoVMManifest("x-w", "x", "2560Mi", httpISO(), d)
	if n := strings.Count(m, "emptyDisk:"); n != 3 {
		t.Fatalf("emptyDisk volumes = %d, want 3:\n%s", n, m)
	}
	if n := strings.Count(m, "capacity: 20Gi"); n != 3 {
		t.Fatalf("capacity: 20Gi count = %d, want 3", n)
	}
	if strings.Contains(m, "storage: 10Gi") || !strings.Contains(m, "storage: 40Gi") {
		t.Fatalf("system disk not 40Gi:\n%s", m)
	}
	if strings.Count(m, "name: spare") != 6 { // a disk and a volume each
		t.Fatalf("spare disks not wired as disk + volume:\n%s", m)
	}

	t.Setenv(demoSpareDisksEnv, "x")
	if _, err := demoDiskConfig(); err == nil {
		t.Fatal("accepted a junk count")
	}
}

// The single-node harness has no Ichor to install Cilium, and the e2e suite asserts the node is ready, so it
// keeps the generator's flannel; the HA harness installs Cilium itself and drops it.
func TestGenerateControlPlaneConfigKeepsFlannelOnRequest(t *testing.T) {
	t.Parallel()

	for _, withFlannel := range []bool{true, false} {
		cfg, err := generateControlPlaneConfig("t", []string{"10.1.2.3"}, withFlannel)
		if err != nil {
			t.Fatal(err)
		}

		got := strings.Contains(string(cfg.machineConfig), "KubeFlannelCNIConfig")
		if got != withFlannel {
			t.Fatalf("flannel in the control-plane config = %v, want %v", got, withFlannel)
		}
	}
}
