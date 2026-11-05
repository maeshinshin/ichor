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

package controller

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// uuidLiteral matches the fixed, UUID-shaped string constants this
// package's tests declare (uuidWeak, uuidClusterReconcile, and friends) to
// key TalosMachine fixtures by name. Several independently-written test
// files in this package each declare their own, and nothing stops two of
// them from picking the same value by coincidence -- which happened twice
// already (once caught here, once on main) before this test existed,
// because envtest's shared "default" namespace means two fixtures with the
// same UUID collide on the same object name the moment both specs run in
// one process.
var uuidLiteral = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// TestFixtureUUIDsAreUnique parses every *_test.go file in this package
// (not just the one being edited) and fails if two constants -- anywhere,
// under any name -- share a UUID-shaped value. It needs no list of known
// constant names to stay in sync with: it finds them by shape, so a new
// uuidFoo added in any file is covered automatically, which a hand-written
// list of "the constants to check" would not be.
func TestFixtureUUIDsAreUnique(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("globbing *_test.go: %v", err)
	}

	byValue := map[string][]string{} // UUID -> "file:line: name"

	fset := token.NewFileSet()
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}

		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}

			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue // value inherited from a previous iota-style spec, never a UUID literal
					}

					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}

					value, err := strconv.Unquote(lit.Value)
					if err != nil || !uuidLiteral.MatchString(value) {
						continue
					}

					where := fset.Position(name.Pos())
					byValue[value] = append(byValue[value], fmt.Sprintf("%s:%d: %s", filepath.Base(where.Filename), where.Line, name.Name))
				}
			}
		}
	}

	for _, v := range slices.Sorted(maps.Keys(byValue)) {
		names := byValue[v]
		if len(names) > 1 {
			t.Errorf("UUID %q is declared by more than one constant, and will collide the moment both specs run in the same envtest namespace:\n\t%s",
				v, strings.Join(names, "\n\t"))
		}
	}
}
