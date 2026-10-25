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

// apireaderScanDirs are the only two places an APIReader field is ever
// wired: cmd/main.go constructs every reconciler, and
// internal/webhook/v1alpha1's SetupTalosMachineWebhookWithManager
// constructs the webhook's validator. Both client.Client (cached) and
// client.Reader's direct implementation satisfy the same client.Reader
// interface APIReader fields are typed as, so a wiring mistake -- writing
// mgr.GetClient() where mgr.GetAPIReader() belongs -- compiles, runs, and
// gives back exactly the silent, dangerous-direction cache staleness's appendix exists to close. Nothing about the
// type system catches
// that; only reading the one call allowed on the right-hand side does.
var apireaderScanDirs = []string{"../../cmd", "../webhook/v1alpha1"}

// TestAPIReaderIsAlwaysTheDirectReader parses every non-test *.go file
// under apireaderScanDirs with go/parser and fails unless every value
// assigned to a field or key named APIReader is exactly a call to a
// method named GetAPIReader -- covering both a struct literal's
// "APIReader: ..." key and a plain "x.APIReader = ..." assignment, the
// two shapes Go allows for setting a field.
func TestAPIReaderIsAlwaysTheDirectReader(t *testing.T) {
	const fieldName = "APIReader"

	fset := token.NewFileSet()
	found := 0

	check := func(pos token.Pos, value ast.Expr) {
		found++

		call, ok := value.(*ast.CallExpr)
		if ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "GetAPIReader" {
				return
			}
		}

		where := fset.Position(pos)
		t.Errorf("%s:%d: %s is assigned %s, not a call to GetAPIReader; APIReader must read directly from the API "+
			"server, never through the manager's cache -- a cached read here can report a "+
			"control plane that was deleted moments ago, which is the dangerous, too-high direction for a quorum count",
			filepath.Base(where.Filename), where.Line, fieldName, astString(value))
	}

	for _, dir := range apireaderScanDirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("globbing %s: %v", dir, err)
		}

		for _, file := range files {
			if isTestFile(file) {
				continue
			}

			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", file, err)
			}

			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.KeyValueExpr:
					if id, ok := node.Key.(*ast.Ident); ok && id.Name == fieldName {
						check(node.Pos(), node.Value)
					}
				case *ast.AssignStmt:
					for i, lhs := range node.Lhs {
						sel, ok := lhs.(*ast.SelectorExpr)
						if ok && sel.Sel.Name == fieldName && i < len(node.Rhs) {
							check(node.Pos(), node.Rhs[i])
						}
					}
				}

				return true
			})
		}
	}

	if found == 0 {
		t.Fatalf("no %s assignment found at all under %v; this guard has nothing to check -- "+
			"did every APIReader field get removed or renamed?", fieldName, apireaderScanDirs)
	}
}

// astString renders expr's source form from the same fset the caller
// already parsed it with, for an error message that shows exactly what
// was written instead of the allowed call.
func astString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				return x.Name + "." + sel.Sel.Name + "(...)"
			}
			return sel.Sel.Name + "(...)"
		}
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	}

	return "an unexpected expression"
}
