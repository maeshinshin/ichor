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

// ichorAPIVersion is the group/version of the TalosCluster these tests build.
const ichorAPIVersion = "ichor.maesh.dev/v1alpha1"

// ichorSystemNS is the namespace Ichor runs in; the TalosCluster of these tests lives there too.
const ichorSystemNS = "ichor-system"

// talosCluster is a TalosCluster in ichor-system with only the sign-in fields the installer reads.
func talosCluster(name string, enabled bool, url string) *unstructured.Unstructured {
	const issuerKey = "ichor"

	u := &unstructured.Unstructured{Object: map[string]any{
		kAPIVersion: ichorAPIVersion,
		kKind:       "TalosCluster",
		kMetadata:   map[string]any{kName: name},
		kSpec: map[string]any{"authentication": map[string]any{issuerKey: map[string]any{
			"enabled": enabled, "url": url,
		}}},
	}}
	u.SetNamespace(ichorSystemNS)

	return u
}

// TestClusterIssuerURLCarriesTheOnlyEnabledCluster: the issuer comes from the TalosCluster that
// enables it; no cluster, several clusters, or a cluster without a URL leave the Deployments alone.
func TestClusterIssuerURLCarriesTheOnlyEnabledCluster(t *testing.T) {
	for _, tc := range []struct {
		name     string
		clusters []runtime.Object
		want     string
	}{
		{"none", nil, ""},
		{"one enabled", []runtime.Object{talosCluster("a", true, "https://ichor.example.com")}, "https://ichor.example.com"},
		{"enabled without a url", []runtime.Object{talosCluster("a", true, "")}, ""},
		{"disabled", []runtime.Object{talosCluster("a", false, "https://ichor.example.com")}, ""},
		{"two enabled", []runtime.Object{
			talosCluster("a", true, "https://a.example.com"), talosCluster("b", true, "https://b.example.com"),
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithRuntimeObjects(tc.clusters...).Build()

			got, err := clusterIssuerURL(context.Background(), c, ichorSystemNS)
			if err != nil {
				t.Fatal(err)
			}

			if got != tc.want {
				t.Errorf("clusterIssuerURL = %q, want %q", got, tc.want)
			}
		})
	}
}
