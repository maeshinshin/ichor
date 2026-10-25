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

package deleteguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// countFuncName is the one function allowed to List TalosMachines for a
// control-plane count. Both of this count's callers -- the webhook's
// admission check and removal.go's own recount under the cluster lock --
// gate a deletion that, done wrong, loses etcd quorum permanently; a second,
// independently-written counting loop is exactly how the cached-client bug
// this guard exists for was introduced in the first place.
const countFuncName = "CountApprovedControlPlanes"

// TestCountApprovedControlPlanesIsTheOnlyListSite parses every non-test
// *.go file in this package with go/parser and fails if any call to a
// method named List appears outside countFuncName's own body. Deliberately
// syntactic (matching on the method name "List", not resolving the
// receiver's type via go/types), the same trade-off
// TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite and
// TestFixtureUUIDsAreUnique (internal/controller) already make.
//
// If a quorum or removal test ever flakes, suspect the count's read path
// (a cached client vs. a direct one) before the environment: that is
// exactly how the bug this guard exists for was found.
func TestCountApprovedControlPlanesIsTheOnlyListSite(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing *.go: %v", err)
	}

	fset := token.NewFileSet()
	found := 0

	for _, file := range files {
		if isTestFile(file) {
			continue
		}

		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "List" {
					return true
				}

				found++

				if fn.Name.Name != countFuncName {
					where := fset.Position(call.Pos())
					t.Errorf("%s:%d: calls List directly from %s; a control-plane count must go "+
						"through %s -- the direct-read requirement (not the manager's cached client) lives there",
						filepath.Base(where.Filename), where.Line, fn.Name.Name, countFuncName)
				}

				return true
			})
		}
	}

	if found == 0 {
		t.Fatalf("no call to List found at all; this guard has nothing to check -- "+
			"did %s get renamed or removed?", countFuncName)
	}
}

func isTestFile(name string) bool {
	return len(name) > len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go"
}
