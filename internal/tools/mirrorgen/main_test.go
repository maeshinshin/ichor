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
	"encoding/json"
	"os"
	"slices"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// crdPath is the CRD "make manifests" renders for TalosConfigPatch, relative
// to this package.
const crdPath = "../../../config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml"

// TestAllowListMatchesCRDEnum checks that roots (the generator's
// allow-list) and the CRD's documents[].kind enum (api/v1alpha1's half of
// the pair, see the comment on rootKind) name exactly the same kinds. Run
// "make manifests" before this test; it reads the real generated CRD
// rather than a second hand-maintained copy of the enum.
func TestAllowListMatchesCRDEnum(t *testing.T) {
	want := make([]string, 0, len(roots))
	for _, r := range roots {
		want = append(want, r.kind)
	}

	slices.Sort(want)

	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("reading generated CRD (run `make manifests` first): %v", err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing CRD: %v", err)
	}

	if len(crd.Spec.Versions) == 0 {
		t.Fatalf("CRD %s declares no versions", crdPath)
	}

	for _, v := range crd.Spec.Versions {
		kindSchema := v.Schema.OpenAPIV3Schema.
			Properties["spec"].
			Properties["documents"].
			Items.Schema.
			Properties["kind"]

		got := make([]string, 0, len(kindSchema.Enum))

		for _, e := range kindSchema.Enum {
			var s string
			if err := json.Unmarshal(e.Raw, &s); err != nil {
				t.Fatalf("version %s: decoding kind enum value %q: %v", v.Name, e.Raw, err)
			}

			got = append(got, s)
		}

		slices.Sort(got)

		if !slices.Equal(want, got) {
			t.Errorf("version %s: CRD documents[].kind enum %v does not match mirrorgen's allow-list %v", v.Name, got, want)
		}
	}
}
