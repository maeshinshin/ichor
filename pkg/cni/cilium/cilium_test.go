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

package cilium

import (
	"context"
	"slices"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
)

func TestSupportsKubeProxyReplacement(t *testing.T) {
	if !New().SupportsKubeProxyReplacement() {
		t.Fatal("SupportsKubeProxyReplacement() = false, want true")
	}
}

func TestTalosPatches(t *testing.T) {
	p := New()

	patches, err := p.TalosPatches(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("TalosPatches(enabled): %v", err)
	}
	if patches != nil {
		t.Fatalf("TalosPatches(enabled) = %v, want nil: kube-proxy enabled is Talos's own default", patches)
	}

	patches, err = p.TalosPatches(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatalf("TalosPatches(replaced): %v", err)
	}
	if len(patches) != 1 {
		t.Fatalf("TalosPatches(replaced) returned %d documents, want 1", len(patches))
	}
	doc := patches[0]
	if doc.Kind != "Config" || doc.Config == nil || doc.Config.ClusterConfig == nil ||
		doc.Config.ClusterConfig.ProxyConfig == nil || doc.Config.ClusterConfig.ProxyConfig.Disabled == nil ||
		!*doc.Config.ClusterConfig.ProxyConfig.Disabled {
		t.Fatalf("TalosPatches(replaced) = %+v, want a Config document with cluster.proxy.disabled = true", doc)
	}
}

func TestArgoCDApplication(t *testing.T) {
	p := New()

	app, err := p.ArgoCDApplication(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatalf("ArgoCDApplication: %v", err)
	}
	if app == nil {
		t.Fatal("ArgoCDApplication = nil, want a rendered Application")
	}

	if got := app.APIVersion; got != "argoproj.io/v1alpha1" {
		t.Errorf("apiVersion = %q, want argoproj.io/v1alpha1", got)
	}
	if got := app.Kind; got != "Application" {
		t.Errorf("kind = %q, want Application", got)
	}

	if app.Spec.SyncPolicy == nil || !slices.Contains(app.Spec.SyncPolicy.SyncOptions, "ServerSideApply=true") {
		t.Errorf("syncOptions = %v, want ServerSideApply=true (large CRD set pitfall)", app.Spec.SyncPolicy)
	}

	if app.Spec.Source == nil || app.Spec.Source.Helm == nil {
		t.Fatal("spec.source.helm is nil")
	}

	if got := app.Spec.IgnoreDifferences; len(got) != 1 || got[0].Group != "cilium.io" || got[0].Kind != "CiliumIdentity" {
		t.Errorf("ignoreDifferences = %+v, want exactly cilium.io/CiliumIdentity", got)
	}
	if slices.Contains(app.Spec.SyncPolicy.SyncOptions, "RespectIgnoreDifferences=true") {
		t.Errorf("syncOptions = %v, want no RespectIgnoreDifferences=true", app.Spec.SyncPolicy.SyncOptions)
	}
	if !slices.Contains(app.Spec.SyncPolicy.SyncOptions, "CreateNamespace=true") {
		t.Errorf("syncOptions = %v, want CreateNamespace=true kept", app.Spec.SyncPolicy.SyncOptions)
	}

	// The handed-over Application renders the same values as the
	// bootstrap manifest, so there is no diff at hand-over.
	boot, err := sigsyaml.Marshal(p.valuesFor(cni.KubeProxyReplaced))
	if err != nil {
		t.Fatalf("marshalling values: %v", err)
	}
	if app.Spec.Source.Helm.Values != string(boot) {
		t.Errorf("Application values differ from the bootstrap values:\n%s\n---\n%s", app.Spec.Source.Helm.Values, boot)
	}
	if app.Spec.Destination.Namespace != namespace {
		t.Errorf("destination namespace = %q, want kube-system", app.Spec.Destination.Namespace)
	}

	values := app.Spec.Source.Helm.Values
	if !strings.Contains(values, "method: cronJob") {
		t.Errorf("spec.source.helm.values missing Hubble's cronJob certificate method:\n%s", values)
	}
}

func TestCheckConsistency(t *testing.T) {
	p := New()

	cases := []struct {
		name    string
		live    cni.LiveConfig
		mode    cni.KubeProxyMode
		wantErr bool
	}{
		{"enabled matches", cni.LiveConfig{KubeProxyReplacement: false}, cni.KubeProxyEnabled, false},
		{"replaced matches", cni.LiveConfig{KubeProxyReplacement: true}, cni.KubeProxyReplaced, false},
		{
			"declares replaced but live still has kube-proxy",
			cni.LiveConfig{KubeProxyReplacement: false}, cni.KubeProxyReplaced, true,
		},
		{
			"declares enabled but live already replaced it",
			cni.LiveConfig{KubeProxyReplacement: true}, cni.KubeProxyEnabled, true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.CheckConsistency(context.Background(), tc.live, tc.mode)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CheckConsistency(%+v, %s) error = %v, wantErr %v", tc.live, tc.mode, err, tc.wantErr)
			}
		})
	}
}

// TestValues_TalosRequiredFive pins the five settings Cilium needs on Talos: ipam, kubeProxyReplacement,
// securityContext.capabilities,
// cgroup, and KubePrism's k8sServiceHost/Port.
func TestValues_TalosRequiredFive(t *testing.T) {
	out, err := sigsyaml.Marshal(New().valuesFor(cni.KubeProxyReplaced))
	if err != nil {
		t.Fatalf("marshalling values: %v", err)
	}
	for _, want := range []string{
		"mode: kubernetes", "kubeProxyReplacement: true", "ciliumAgent:", "cleanCiliumState:", "SYS_ADMIN",
		"enabled: false", "hostRoot: /sys/fs/cgroup", "k8sServiceHost: localhost", "k8sServicePort: 7445",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("values missing %q:\n%s", want, out)
		}
	}
}

func TestHandoverWorkloads(t *testing.T) {
	workloads := New().HandoverWorkloads()
	if len(workloads) == 0 {
		t.Fatal("HandoverWorkloads() is empty, want the cilium agent and operator")
	}

	var sawDaemonSet, sawDeployment bool
	for _, w := range workloads {
		if w.Namespace != "kube-system" {
			t.Errorf("workload %+v: namespace = %q, want kube-system", w, w.Namespace)
		}
		switch w.Kind {
		case "DaemonSet":
			sawDaemonSet = true
		case "Deployment":
			sawDeployment = true
		}
	}
	if !sawDaemonSet || !sawDeployment {
		t.Fatalf("HandoverWorkloads() = %v, want a DaemonSet (agent) and a Deployment (operator)", workloads)
	}
}

// Addendum: without syncPolicy.automated Argo CD never syncs the
// Application and the hand-over never completes.
func TestArgoCDApplication_AutomatedSync(t *testing.T) {
	app, err := New().ArgoCDApplication(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatalf("ArgoCDApplication: %v", err)
	}

	sp := app.Spec.SyncPolicy
	if sp == nil || sp.Automated == nil || !sp.Automated.Prune || !sp.Automated.SelfHeal {
		t.Fatalf("syncPolicy = %+v, want automated {prune: true, selfHeal: true}", sp)
	}

	if !slices.Contains(sp.SyncOptions, "PruneLast=true") {
		t.Errorf("syncOptions = %v, want PruneLast=true", sp.SyncOptions)
	}
}
