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
	"fmt"
	"go/types"
	"os"
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// crdPath is the CRD "make manifests" renders for TalosConfigPatch, relative
// to this package.
const crdPath = "../../../config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml"

// TestShippedMatchesCRDEnum checks that shipped (the generator's list) and the CRD's documents[].kind enum
// name exactly the same kinds. Run "make manifests" before this test; it reads the real generated CRD. A
// kind dropped from shipped but still in the committed CRD fails here until the CRD is regenerated, and
// the regenerated CRD then shows the removed enum value in the diff.
func TestShippedMatchesCRDEnum(t *testing.T) {
	want := make([]string, 0, len(shipped))
	for _, s := range shipped {
		want = append(want, ichorName(s.kind))
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
			t.Errorf("version %s: CRD documents[].kind enum %v does not match mirrorgen's shipped kinds %v", v.Name, got, want)
		}
	}
}

// TestSecretFieldsAreNotInCRD is secretFields' own correctness check,
// the same role the round-trip tests play for substTable: run the real
// generator (not a hand-maintained second copy of what it should have
// dropped) and confirm that none of the YAML names it reports dropping
// for secretFields reasons appear anywhere in the real generated CRD,
// with a matching shape. This is what should have caught
// EncryptionKey.KeyStatic silently surviving under its renamed mirror
// name (VolumeEncryptionKey): the generator's own report already said
// "KeyStatic (yaml "static")" correctly, but nothing checked the CRD
// actually agreed. Run "make manifests" before this test; adding a
// secretFields row needs no change here to stay covered.
//
// Matching requires the candidate property's shape (object vs. scalar) to
// match the dropped field's own shape, not just the bare YAML name: two
// unrelated fields can legitimately share a YAML name at different schema
// depths. This test actually hit both directions once each --
// machine.registries.*.tls.ca is an unrelated, legitimate scalar also
// named "ca" (the dropped ClusterCA/MachineCA/RootCA are object-shaped key
// pairs); RegistryConfig's own "auth" field is an unrelated, legitimate
// object (still has "username") also named "auth" (the dropped
// RegistryAuthConfig.RegistryAuth is a scalar, one level deeper inside
// that same object) -- shape is what tells each pair apart without
// tracking full schema paths.
func TestSecretFieldsAreNotInCRD(t *testing.T) {
	g := generate()

	if len(g.droppedSecret) == 0 {
		t.Fatal("generate() reported no secretFields drops; secretFields is empty or the walk didn't run")
	}

	raw, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("reading generated CRD (run `make manifests` first): %v", err)
	}

	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatalf("parsing CRD: %v", err)
	}

	for _, v := range crd.Spec.Versions {
		var found []string
		schemaProperties(v.Schema.OpenAPIV3Schema, func(name string, isObject bool) {
			for _, d := range g.droppedSecret {
				if name == d.YAML && isObject == !d.Scalar {
					found = append(found, fmt.Sprintf("%q (%s.%s: %s)", d.YAML, d.Struct, d.Field, d.Why))
				}
			}
		})

		if len(found) > 0 {
			t.Errorf("version %s: CRD schema still has a property secretFields says it dropped: %v", v.Name, found)
		}
	}
}

// schemaProperties calls visit with every property name anywhere in
// schema's tree, along with whether that property is itself an object (has
// its own nested properties) rather than a scalar, recursing through
// nested objects, array items and additionalProperties.
func schemaProperties(schema *apiextensionsv1.JSONSchemaProps, visit func(name string, isObject bool)) {
	if schema == nil {
		return
	}

	for name, prop := range schema.Properties {
		visit(name, len(prop.Properties) > 0)
		schemaProperties(&prop, visit)
	}

	if schema.Items != nil {
		schemaProperties(schema.Items.Schema, visit)
	}

	if schema.AdditionalProperties != nil {
		schemaProperties(schema.AdditionalProperties.Schema, visit)
	}
}

// TestEmbeddedStructsAreFlattened: a struct embedded with the yaml inline option has its fields written at the
// top level of the embedding document, so the mirror carries them there, in the order the document has them,
// and looks each one up in secretFields under the struct that declares it.
func TestEmbeddedStructsAreFlattened(t *testing.T) {
	const pkg = "github.com/maeshinshin/ichor/internal/tools/mirrorgen/testdata/embedded"

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}, pkg)
	if err != nil || len(pkgs[0].Errors) > 0 {
		t.Fatalf("loading %s: %v %v", pkg, err, pkgs[0].Errors)
	}

	secretFields["Common.MTU"] = "test"

	defer delete(secretFields, "Common.MTU")

	g := newGenerator(pkgs)
	named := g.lookupType(pkg, "Outer")
	g.structMapping(named, named.Underlying().(*types.Struct))

	var got []string

	for _, f := range g.structs[named].fields {
		got = append(got, f.name+" "+f.jsonName)
	}

	want := []string{"Name name", "Up up", "Extra extra"} // MTU is dropped by its declaring struct's secretFields row
	if !slices.Equal(got, want) {
		t.Errorf("fields of Outer: got %q, want %q", got, want)
	}
}
