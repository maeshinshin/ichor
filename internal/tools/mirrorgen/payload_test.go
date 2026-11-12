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

// TestShippedKindsKeepTheirPayload fails, naming the kind and the field, for a shipped kind whose mirror has no
// fields or that drops a meta.Unstructured field nobody decided to drop (see droppedPayloads).
func TestShippedKindsKeepTheirPayload(t *testing.T) {
	if problems := generate().payloadProblems(droppedPayloads); len(problems) > 0 {
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

	rows := map[string]string{"WithPayload.Raw": "test", "OnlyPayload.Raw": "test", "Gone.Raw": "test"}

	got = strings.Join(g.payloadProblems(rows), "\n")
	if strings.Contains(got, "meta.Unstructured field") || !strings.Contains(got, `"Gone.Raw" matches no dropped field`) {
		t.Errorf("with the decisions recorded the gate should refuse only the empty mirror and the stale row, got:\n%s", got)
	}
}
