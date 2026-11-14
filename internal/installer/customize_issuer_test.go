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

package installer

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// talosCluster is a TalosCluster with only the sign-in fields the installer reads.
func talosCluster(name, ns string, enabled bool, url string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ichor.maesh.dev/v1alpha1",
		"kind":       "TalosCluster",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{"authentication": map[string]any{"ichor": map[string]any{
			"enabled": enabled, "url": url,
		}}},
	}}
}

// TestClusterIssuerURLCarriesTheOnlyEnabledCluster: the issuer comes from the TalosCluster that
// enables it; no cluster, several clusters, or a cluster without a URL leave the Deployments alone.
func TestClusterIssuerURLCarriesTheOnlyEnabledCluster(t *testing.T) {
	const ns = "ichor-system"

	for _, tc := range []struct {
		name     string
		clusters []runtime.Object
		want     string
	}{
		{"none", nil, ""},
		{"one enabled", []runtime.Object{talosCluster("a", ns, true, "https://ichor.example.com")}, "https://ichor.example.com"},
		{"enabled without a url", []runtime.Object{talosCluster("a", ns, true, "")}, ""},
		{"disabled", []runtime.Object{talosCluster("a", ns, false, "https://ichor.example.com")}, ""},
		{"two enabled", []runtime.Object{
			talosCluster("a", ns, true, "https://a.example.com"), talosCluster("b", ns, true, "https://b.example.com"),
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithRuntimeObjects(tc.clusters...).Build()

			got, err := clusterIssuerURL(context.Background(), c, ns)
			if err != nil {
				t.Fatal(err)
			}

			if got != tc.want {
				t.Errorf("clusterIssuerURL = %q, want %q", got, tc.want)
			}
		})
	}
}
