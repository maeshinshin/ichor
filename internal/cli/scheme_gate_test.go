package cli

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	authnv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// A client the CLI builds has cliScheme, and a test client must have the same: a hand-built scheme passes tests for a
// type the real client cannot read or write ("no kind is registered for the type v1.Job"). Tests may extend
// cliScheme, never replace it.
func TestEveryFakeClientInThisPackageUsesTheCLIScheme(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files: %v", err)
	}

	fromCLI := map[string]bool{"newTestScheme": true, "cliScheme": true}

	for _, name := range files {
		fset := token.NewFileSet()

		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		// Identifiers whose every assignment in the file is a call of newTestScheme or cliScheme.
		ok, bad := map[string]bool{}, map[string]bool{}

		ast.Inspect(f, func(n ast.Node) bool {
			a, isAssign := n.(*ast.AssignStmt)
			if !isAssign {
				return true
			}

			for i, l := range a.Lhs {
				id, isIdent := l.(*ast.Ident)
				if !isIdent || id.Name == "_" || id.Name == "err" {
					continue
				}

				var rhs ast.Expr
				if len(a.Rhs) == len(a.Lhs) {
					rhs = a.Rhs[i]
				} else if len(a.Rhs) == 1 {
					rhs = a.Rhs[0] // scheme, err := cliScheme()
				}

				if call, isCall := rhs.(*ast.CallExpr); isCall {
					if fn, isFn := call.Fun.(*ast.Ident); isFn && fromCLI[fn.Name] {
						ok[id.Name] = true

						continue
					}
				}

				bad[id.Name] = true
			}

			return true
		})

		// Each fake.NewClientBuilder() chain must carry WithScheme(<the CLI scheme>).
		var stack []ast.Node

		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]

				return true
			}

			stack = append(stack, n)

			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}

			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "NewClientBuilder" {
				return true
			}

			found := false

			// Walk outwards through the method chain: X.M1(...).M2(...).
			for i := len(stack) - 2; i >= 1; i-- {
				outer, isSel := stack[i].(*ast.SelectorExpr)
				if !isSel {
					break
				}

				if outer.Sel.Name == "WithScheme" {
					found = true

					if c, isCall := stack[i-1].(*ast.CallExpr); isCall && len(c.Args) == 1 && !schemeFromCLI(c.Args[0], fromCLI, ok, bad) {
						t.Errorf("%s: the fake client's scheme is not cliScheme(): %s", fset.Position(call.Pos()), exprString(fset, c.Args[0]))
					}
				}
			}

			if !found {
				t.Errorf("%s: a fake client with no WithScheme(cliScheme()) falls back to client-go's scheme, which has no Ichor types", fset.Position(call.Pos()))
			}

			return true
		})
	}
}

func schemeFromCLI(e ast.Expr, fromCLI, ok, bad map[string]bool) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		fn, isFn := v.Fun.(*ast.Ident)

		return isFn && fromCLI[fn.Name]
	case *ast.Ident:
		return ok[v.Name] && !bad[v.Name]
	}

	return false
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	return fset.Position(e.Pos()).String()
}

// The CLI creates Jobs (the installer check) and, in local mode, SelfSubjectReviews; a client built on cliScheme must
// know them.
func TestCLISchemeCreatesAJob(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	job := &batchv1.Job{}
	job.Namespace, job.Name = "ns", "j"

	if err := c.Create(context.Background(), job); err != nil {
		t.Fatalf("creating a Job through a client on cliScheme: %v", err)
	}

	if _, _, err := newTestScheme(t).ObjectKinds(&authnv1.SelfSubjectReview{}); err != nil {
		t.Errorf("SelfSubjectReview (reclaim's `who am I`, in local mode) is not registered: %v", err)
	}
}
