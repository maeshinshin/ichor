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
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// acquireLockFuncName is the one function allowed to call
// clusterlock.Lock.Acquire. Every disruptive operation (removal today,
// the TalosOperation backbone; a future upgrade or CA rotation) must go through it, so the
// control-plane-transitioning refusal lives in one
// place rather than being a safety net each new call site has to
// remember to add -- the same way the secrets RBAC marker regressed once
// already by not being centralized.
const acquireLockFuncName = "acquireDisruptiveLock"

// TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite parses every
// non-test *.go file in this package with go/parser and fails if any
// call to a method named Acquire appears outside acquireLockFuncName's
// own body. It is deliberately syntactic (matching on the method name
// "Acquire", not resolving clusterlock.Lock's type via go/types): the
// only type in this package's dependency graph with a method by that
// name is clusterlock.Lock, so the shape is unambiguous, the same
// trade-off TestFixtureUUIDsAreUnique already makes for UUID-shaped
// string literals.
func TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing *.go: %v", err)
	}

	fset := token.NewFileSet()
	found := 0

	for _, file := range files {
		if isTestFile(file) {
			continue // tests legitimately call Acquire directly to simulate another holder
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
				if !ok || sel.Sel.Name != "Acquire" {
					return true
				}

				found++

				if fn.Name.Name != acquireLockFuncName {
					where := fset.Position(call.Pos())
					t.Errorf("%s:%d: calls Acquire directly from %s; disruptive operations must go "+
						"through %s -- the even-control-plane-count refusal lives there",
						filepath.Base(where.Filename), where.Line, fn.Name.Name, acquireLockFuncName)
				}

				return true
			})
		}
	}

	if found == 0 {
		t.Fatalf("no call to Acquire found at all; this guard has nothing to check -- "+
			"did %s get renamed or removed?", acquireLockFuncName)
	}
}

func isTestFile(name string) bool {
	return len(name) > len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go"
}
