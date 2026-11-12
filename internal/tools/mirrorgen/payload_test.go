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
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// testReason is the reason column of the rows the tests invent.
const testReason = "recorded for this test"

// TestShippedKindsKeepTheirPayload fails, naming the kind and the field, for a shipped kind whose mirror has no
// fields, that drops a meta.Unstructured field nobody decided to drop (see droppedPayloads), or whose subset
// override or refused field has a row that matches nothing (see omissionProblems).
func TestShippedKindsKeepTheirPayload(t *testing.T) {
	g := generate()

	if problems := append(g.payloadProblems(droppedPayloads), g.omissionProblems(fieldOverrides, refusedFields)...); len(problems) > 0 {
		t.Errorf("a shipped kind would be accepted with its content discarded:\n  %s", strings.Join(problems, "\n  "))
	}
}

// TestPayloadGateRefusesWhatItShould feeds the gate a kind with a dropped payload field and a kind that is
// nothing but one, and checks each is refused by name until a row records the decision.
func TestPayloadGateRefusesWhatItShould(t *testing.T) {
	const pkg = "github.com/maeshinshin/ichor/internal/tools/mirrorgen/testdata/embedded"

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}, pkg)
	if err != nil || len(pkgs[0].Errors) > 0 {
		t.Fatalf("loading %s: %v %v", pkg, err, pkgs[0].Errors)
	}

	g := newGenerator(pkgs)

	for _, name := range []string{"WithPayload", "OnlyPayload"} {
		named := g.lookupType(pkg, name)
		g.structMapping(named, named.Underlying().(*types.Struct))

		def := g.structs[named]
		def.isRoot, def.rootKind = true, rootKind{kind: name}
	}

	got := strings.Join(g.payloadProblems(nil), "\n")
	for _, want := range []string{
		`kind WithPayload: WithPayload.Raw (yaml "raw")`,
		`kind OnlyPayload: OnlyPayload.Raw (yaml "raw")`,
		"kind OnlyPayload: its mirror OnlyPayload has no fields",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the gate did not report %q; it reported:\n%s", want, got)
		}
	}

	rows := map[string]string{"WithPayload.Raw": testReason, "OnlyPayload.Raw": testReason, "Gone.Raw": testReason}

	got = strings.Join(g.payloadProblems(rows), "\n")
	if strings.Contains(got, "meta.Unstructured field") || !strings.Contains(got, `"Gone.Raw" matches no dropped field`) {
		t.Errorf("with the decisions recorded the gate should refuse only the empty mirror and the stale row, got:\n%s", got)
	}
}

// TestSubsetOverrideKeepsWhatIsNotOmitted builds a subset of a small upstream struct and checks that every field
// is either kept or named in the omits table, that a nested struct with a row of its own becomes a local struct,
// and that a row naming no field, and a refused row matching nothing, are reported.
func TestSubsetOverrideKeepsWhatIsNotOmitted(t *testing.T) {
	const pkg = "github.com/maeshinshin/ichor/internal/tools/mirrorgen/testdata/embedded"

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedDeps | packages.NeedImports,
	}, pkg)
	if err != nil || len(pkgs[0].Errors) > 0 {
		t.Fatalf("loading %s: %v %v", pkg, err, pkgs[0].Errors)
	}

	g := newGenerator(pkgs)
	omits := map[string]string{
		"Upstream.drop": testReason, "Inner.b": testReason, "Upstream.gone": testReason,
	}
	s := g.subsetFor(g.lookupType(pkg, "Upstream"), omits)

	kept := make([]string, 0, len(s.fields))
	for _, f := range s.fields {
		kept = append(kept, f.jsonName+" "+f.typeExpr)
	}

	if got, want := strings.Join(kept, ","), "keep string,nested *InnerSubset,when *metav1.Time"; got != want {
		t.Errorf("kept fields %q, want %q", got, want)
	}

	if got, want := strings.Join(s.omitted, ","), "drop"; got != want {
		t.Errorf("omitted fields %q, want %q", got, want)
	}

	got := strings.Join(g.omissionProblems(map[string]fieldOverride{"x": {omits: omits}}, map[string]string{"Nothing.Here": testReason}), "\n")
	for _, want := range []string{`omits row "Upstream.gone" names no field`, `refusedFields row "Nothing.Here" matches no field`} {
		if !strings.Contains(got, want) {
			t.Errorf("the gate did not report %q; it reported:\n%s", want, got)
		}
	}
}
