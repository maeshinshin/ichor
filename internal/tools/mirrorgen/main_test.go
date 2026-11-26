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
	"fmt"
	"go/types"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// crdPath is the CRD "make manifests" renders for TalosConfigPatch, relative
// to this package.
const crdPath = "../../../config/crd/bases/ichor.maesh.dev_talosconfigpatches.yaml"

// TestShippedKindsAreTheKindsSpecConfigConvertsTo checks that shipped (the generator's list) and the kinds the
// tags of Ichor's schema name are exactly the same: a kind dropped from shipped but still written by the schema, or
// shipped but placed nowhere, fails here until the schema is regenerated.
func TestShippedKindsAreTheKindsSpecConfigConvertsTo(t *testing.T) {
	want := make([]string, 0, len(shipped))
	for _, s := range shipped {
		want = append(want, ichorName(s.kind))
	}

	slices.Sort(want)

	kinds := map[string]bool{}
	tagKinds(reflect.TypeFor[ichorconfig.Config](), kinds, map[reflect.Type]bool{})

	got := slices.Sorted(maps.Keys(kinds))

	if !slices.Equal(want, got) {
		t.Errorf("the schema's tags name %v, mirrorgen ships %v", got, want)
	}
}

// tagKinds collects the document kinds named by the ichor tags below t.
func tagKinds(t reflect.Type, kinds map[string]bool, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}

	seen[t] = true

	for f := range t.Fields() {
		for part := range strings.SplitSeq(strings.TrimPrefix(f.Tag.Get("ichor"), "docs="), ";") {
			if kind, _, ok := strings.Cut(part, ":"); ok {
				kinds[strings.TrimPrefix(kind, "+")] = true
			} else if part != "" {
				kinds[part] = true
			}
		}

		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice || ft.Kind() == reflect.Map {
			ft = ft.Elem()
		}

		if ft.Kind() == reflect.Struct && ft.PkgPath() == t.PkgPath() {
			tagKinds(ft, kinds, seen)
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

	secretFields["Common.MTU"] = testReason

	defer delete(secretFields, "Common.MTU")

	g := newGenerator(pkgs)
	named := g.lookupType(pkg, "Outer")
	g.structMapping(named, named.Underlying().(*types.Struct))

	got := make([]string, 0, 4)

	for _, f := range g.structs[named].fields {
		got = append(got, f.name+" "+f.jsonName)
	}

	want := []string{"Name name", "Up up", "Extra extra"} // MTU is dropped by its declaring struct's secretFields row
	if !slices.Equal(got, want) {
		t.Errorf("fields of Outer: got %q, want %q", got, want)
	}
}

// TestFieldsTalosDefaultsAreOptional: Talos documents these fields as optional or defaulted although its struct tags
// carry no omitempty, so a patch that leaves one out must be admitted.
func TestFieldsTalosDefaultsAreOptional(t *testing.T) {
	want := []string{
		"Route.RouteGateway", "InstallConfig.InstallWipe", "DHCPOptions.DHCPRouteMetric", "Vlan.VlanRoutes",
		"ClusterNetworkConfig.DNSDomain", "KubeNetworkConfigV1Alpha1.NetworkDNSDomain",
	}

	got := map[string]bool{}

	for _, s := range generate().structs {
		for _, f := range s.fields {
			got[s.machName+"."+f.name] = f.optional
		}
	}

	for _, k := range want {
		if optional, ok := got[k]; !ok || !optional {
			t.Errorf("%s: want an optional mirror field (present=%v)", k, ok)
		}
	}
}
