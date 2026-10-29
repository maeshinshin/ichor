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
	"strings"
	"testing"
	"time"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// TestF1CiliumHelm (ADR 0104 F1, with ADR 0105: a Helm install gets an IchorInstallation with manager helm): on a cluster
// with a Helm-installed Cilium, helm install of the packaged chart, adopt skips the install step, registers the
// cluster and releases the hold; helm upgrade works; helm uninstall keeps the CRDs and the cluster's objects.
func TestF1CiliumHelm(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1, CNI: foreign.CNICiliumHelm})
	start := time.Now()
	if out, err := helmInstall(t, c, true); err != nil {
		t.Fatalf("helm install failed: %v\n%s", err, out)
	}
	t.Logf("helm install took %s", time.Since(start))
	inst, _ := inner(c, "get", "ichorinstallations.ichor.maesh.dev", "-o", "jsonpath={.items[*].spec.deployment.manager}")
	t.Logf("IchorInstallation managers before adopt: %q", inst)

	start = time.Now()
	out, err := adopt(t, c, "--cni-unmanaged")
	t.Logf("adopt took %s", time.Since(start))
	if err != nil {
		t.Fatalf("adopt failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed by Helm") || strings.Contains(out, "applied Ichor's own install manifest") {
		t.Errorf("adopt did not skip the install step")
	}
	membership(t, c)
	holdCleared(t, c)
	if got, _ := inner(c, "get", "ichorinstallations.ichor.maesh.dev", "-o", "jsonpath={.items[*].spec.deployment.manager}"); strings.TrimSpace(got) != "helm" {
		t.Errorf("IchorInstallation manager = %q, want helm", got)
	}
	if rel, _ := local(t, time.Minute, "helm", "list", "-n", crNS, "--kubeconfig", c.LocalKube); !strings.Contains(rel, "deployed") {
		t.Errorf("helm list does not show the release deployed")
	}

	// helm upgrade works against the adopted cluster.
	if out, err := local(t, 8*time.Minute, "helm", "upgrade", "ichor", packagedChart(t), "--kubeconfig", c.LocalKube, "-n", crNS,
		"--reuse-values", "--set", "manager.replicas=1", "--wait", "--timeout", "6m"); err != nil {
		t.Errorf("helm upgrade failed: %v\n%s", err, out)
	}
	if got, _ := inner(c, "-n", crNS, "get", "deploy", "-l", "control-plane=controller-manager", "-o", "jsonpath={.items[0].spec.replicas}"); got != "1" {
		t.Errorf("controller replicas after helm upgrade = %q, want 1", got)
	}

	// helm uninstall removes the release but keeps the CRDs and the cluster's objects.
	if out, err := local(t, 5*time.Minute, "helm", "uninstall", "ichor", "-n", crNS, "--kubeconfig", c.LocalKube); err != nil {
		t.Errorf("helm uninstall failed: %v\n%s", err, out)
	}
	crds, _ := inner(c, "get", "crd", "--no-headers")
	if !strings.Contains(crds, "talosclusters.ichor.maesh.dev") {
		t.Errorf("the CRDs are gone after helm uninstall:\n%s", crds)
	}
	if got := clusterJSONPath(c, "{.metadata.name}"); got != c.Opts.Prefix {
		t.Errorf("the TalosCluster is gone after helm uninstall (%q)", got)
	}
	if ms := names(c, "-n", crNS, "get", "talosmachines.ichor.maesh.dev"); len(ms) != 2 {
		t.Errorf("TalosMachines after helm uninstall = %v, want 2", ms)
	}
	if sec, _ := inner(c, "-n", crNS, "get", "secret", c.Opts.Prefix+"-secrets", "--no-headers"); strings.TrimSpace(sec) == "" {
		t.Errorf("the cluster's secrets Secret is gone after helm uninstall")
	}
	nodesReady(t, c)
}
