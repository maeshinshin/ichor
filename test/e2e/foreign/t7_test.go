//go:build e2e
// +build e2e

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

package foreign

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
	"github.com/maeshinshin/ichor/test/internal/kube"
)

const ichorInstallation = "ichorinstallations.ichor.maesh.dev"

// installationField reads one jsonpath of the singleton IchorInstallation.
func installationField(c *foreign.Cluster, path string) string {
	out, _ := inner(c, "get", ichorInstallation, "ichor", "-o", "jsonpath="+path)
	return strings.TrimSpace(out)
}

// ichorInPod runs `ichor <args>` in the tools pod with the cluster's kubeconfig (the settings commands take neither
// --local nor -n).
func ichorInPod(t *testing.T, c *foreign.Cluster, args ...string) (string, error) {
	t.Helper()
	pod := stage(t, c)
	a := append([]string{"--context", os.Getenv(kube.ContextEnv), "-n", ns, "exec", pod, "--", "env", "HOME=/tmp/home", "/tmp/ichor"}, args...)
	a = append(a, "--kubeconfig", "/tmp/kubeconfig")
	out, err := exec.Command("kubectl", a...).CombinedOutput()
	t.Logf("ichor %s: %v\n%s", strings.Join(args, " "), err, out)
	return string(out), err
}

// TestT7HelmManager: a helm install of the packaged chart gets a seeded IchorInstallation with
// manager helm; `ichor upgrade ichor` refuses and prints the helm command; a Settings edit survives helm upgrade.
func TestT7HelmManager(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	if out, err := helmInstall(t, c, true); err != nil {
		t.Fatalf("helm install failed: %v\n%s", err, out)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for installationField(c, "{.spec.deployment.manager}") == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
	}
	if got := installationField(c, "{.spec.deployment.manager}"); got != "helm" {
		t.Fatalf("seeded IchorInstallation manager = %q, want helm", got)
	}
	t.Logf("seeded spec.version = %q", installationField(c, "{.spec.version}"))

	out, err := ichorInPod(t, c, "upgrade", "ichor", "v0.0.1-g0000000", "-n", crNS, "--local")
	if err == nil || !strings.Contains(out, "helm upgrade") {
		t.Errorf("ichor upgrade ichor was not refused with the helm command (err=%v)", err)
	}
	if ops, _ := inner(c, "-n", crNS, "get", "talosoperations.ichor.maesh.dev", "-o", "name"); strings.TrimSpace(ops) != "" {
		t.Errorf("a TalosOperation exists after the refused upgrade:\n%s", ops)
	}

	if out, err := ichorInPod(t, c, "settings", "set", "users.tokenTTL=10m"); err != nil {
		t.Fatalf("ichor settings set failed: %v\n%s", err, out)
	}
	if got := installationField(c, "{.spec.users.tokenTTL}"); got != "10m0s" {
		t.Fatalf("spec.users.tokenTTL = %q, want 10m0s", got)
	}
	if out, err := local(t, 8*time.Minute, "helm", "upgrade", "ichor", packagedChart(t), "--kubeconfig", c.LocalKube, "-n", crNS,
		"--reuse-values", "--set", "manager.replicas=1", "--wait", "--timeout", "6m"); err != nil {
		t.Fatalf("helm upgrade failed: %v\n%s", err, out)
	}
	if got := installationField(c, "{.spec.users.tokenTTL}"); got != "10m0s" {
		t.Errorf("spec.users.tokenTTL after helm upgrade = %q, want 10m0s", got)
	}
	if got := installationField(c, "{.spec.deployment.manager}"); got != "helm" {
		t.Errorf("manager after helm upgrade = %q, want helm", got)
	}
}
