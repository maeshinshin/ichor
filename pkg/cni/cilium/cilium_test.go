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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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

func TestBootstrapManifest_CarriesTalosRecommendedValues(t *testing.T) {
	p := New()

	out, err := p.BootstrapManifest(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatalf("BootstrapManifest(enabled): %v", err)
	}
	enabled := string(out)
	for _, want := range []string{"kubeProxyReplacement: false", "hostRoot: /sys/fs/cgroup", "method: cronJob"} {
		if !strings.Contains(enabled, want) {
			t.Errorf("BootstrapManifest(enabled) missing %q:\n%s", want, enabled)
		}
	}
	if strings.Contains(enabled, "k8sServiceHost") {
		t.Errorf("BootstrapManifest(enabled) sets k8sServiceHost, want it absent without kube-proxy replacement:\n%s",
			enabled)
	}

	out, err = p.BootstrapManifest(cni.KubeProxyReplaced)
	if err != nil {
		t.Fatalf("BootstrapManifest(replaced): %v", err)
	}
	replaced := string(out)
	for _, want := range []string{
		"kubeProxyReplacement: true",
		"k8sServiceHost: localhost",
		"k8sServicePort: 7445",
		"NET_ADMIN",
		"method: cronJob",
	} {
		if !strings.Contains(replaced, want) {
			t.Errorf("BootstrapManifest(replaced) missing %q:\n%s", want, replaced)
		}
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

	if got := app.GetAPIVersion(); got != "argoproj.io/v1alpha1" {
		t.Errorf("apiVersion = %q, want argoproj.io/v1alpha1", got)
	}
	if got := app.GetKind(); got != "Application" {
		t.Errorf("kind = %q, want Application", got)
	}

	syncOptions, found, err := unstructured.NestedStringSlice(app.Object, "spec", "syncPolicy", "syncOptions")
	if err != nil || !found {
		t.Fatalf("spec.syncPolicy.syncOptions: found=%v err=%v", found, err)
	}
	if !slices.Contains(syncOptions, "ServerSideApply=true") {
		t.Errorf("syncOptions = %v, want ServerSideApply=true (large CRD set pitfall)", syncOptions)
	}

	values, found, err := unstructured.NestedString(app.Object, "spec", "source", "helm", "values")
	if err != nil || !found {
		t.Fatalf("spec.source.helm.values: found=%v err=%v", found, err)
	}
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
