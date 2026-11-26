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

package chart

import (
	"path/filepath"
	"slices"
	"testing"
)

const namespaceWebhookPath = "/validate-ichor-maesh-dev-v1alpha1-namespace"

// namespacedKinds is the plural of every CRD in config/crd/bases whose scope is Namespaced: the kinds ADR 0175
// keeps in the controller's namespace.
func namespacedKinds(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(crdSourceDir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no CRDs in %s: %v", crdSourceDir, err)
	}

	var out []string

	for _, f := range files {
		for _, crd := range loadYAMLDocs(t, f) {
			spec, _ := crd["spec"].(map[string]any)
			names, _ := spec["names"].(map[string]any)

			if scope, _ := spec["scope"].(string); scope == "Namespaced" {
				plural, _ := names["plural"].(string)
				out = append(out, plural)
			}
		}
	}

	slices.Sort(out)

	return out
}

// checkNamespaceWebhook fails unless a ValidatingWebhookConfiguration in docs sends CREATE of every namespaced kind to
// the controller-namespace check: a kind missing from its rules is a kind that can be created anywhere.
func checkNamespaceWebhook(t *testing.T, label string, docs []map[string]any) {
	t.Helper()

	var got []string

	for _, doc := range findAll(docs, "ValidatingWebhookConfiguration") {
		webhooks, _ := doc["webhooks"].([]any)
		for _, w := range webhooks {
			wm, _ := w.(map[string]any)
			svc, _ := wm["clientConfig"].(map[string]any)["service"].(map[string]any)

			if path, _ := svc["path"].(string); path != namespaceWebhookPath {
				continue
			}

			rules, _ := wm["rules"].([]any)
			for _, r := range rules {
				rm, _ := r.(map[string]any)
				ops, _ := rm["operations"].([]any)

				if !slices.Contains(ops, any("CREATE")) {
					t.Errorf("%s: a rule of %s does not list CREATE: %v", label, namespaceWebhookPath, ops)
				}

				res, _ := rm["resources"].([]any)
				for _, x := range res {
					s, _ := x.(string)
					got = append(got, s)
				}
			}
		}
	}

	slices.Sort(got)

	if want := namespacedKinds(t); !slices.Equal(got, want) {
		t.Errorf("%s: %s covers %v, want every namespaced kind %v", label, namespaceWebhookPath, got, want)
	}
}

func TestGeneratedWebhookManifestCoversEveryNamespacedKind(t *testing.T) {
	checkNamespaceWebhook(t, "config/webhook/manifests.yaml", loadYAMLDocs(t, "../../config/webhook/manifests.yaml"))
}

func TestChartWebhookCoversEveryNamespacedKind(t *testing.T) {
	checkNamespaceWebhook(t, "the chart", renderChart(t, "ichor"))
}
