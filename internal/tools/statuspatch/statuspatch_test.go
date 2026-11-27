// Package statuspatch holds the check that no status write replaces a status array from a stale copy.
//
// A JSON merge patch replaces an array whole, so Status().Patch(ctx, obj, client.MergeFrom(base)) sends
// the whole conditions list the writer holds. A writer with a stale copy erases a condition another
// controller raised since, a one-shot one such as BundleBackupStale. Every such patch must carry
// client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}), so a stale copy conflicts
// instead; a site that must not conflict, or whose object has no list in its status, is named in allowed, with the reason.
//
// It reads the syntax of every non-test file under internal/ and pkg/; no type information is needed.
package statuspatch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../../.."

// site is a file (relative to the repository root) and the function that holds the write.
type site struct{ file, fn string }

var allowed = map[site]string{
	{"internal/controller/kubeletserving.go", "recordIssued"}: "TalosMachine status.kubeletServingCertificate is one pointer, " +
		"written only here; the patch sends no array",
	{"internal/controller/cni_kubeproxy_switch.go", "setTalosSide"}: "TalosCluster status.cni.kubeProxy is one string, " +
		"written only here; the patch sends no array, and a spec edit made just before must not pause the switch",
}

func TestNoUnlockedStatusMergePatch(t *testing.T) {
	fset := token.NewFileSet()
	seen := map[site]bool{}

	for _, dir := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}

			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(repoRoot, path)

			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}

				ast.Inspect(fd, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok && isStatusPatch(call) && hasPlainMergeFrom(call) {
						s := site{filepath.ToSlash(rel), fd.Name.Name}
						seen[s] = true

						if _, ok := allowed[s]; !ok {
							t.Errorf("%s: %s: Status().Patch with client.MergeFrom replaces status arrays from a possibly stale copy; "+
								"use client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}) at %s",
								s.file, s.fn, fset.Position(call.Pos()))
						}
					}

					return true
				})
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for s := range allowed {
		if !seen[s] {
			t.Errorf("allowed site %v has no plain Status().Patch any more; drop it from allowed", s)
		}
	}
}

// isStatusPatch matches X.Status().Patch(...).
func isStatusPatch(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Patch" {
		return false
	}

	inner, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}

	isel, ok := inner.Fun.(*ast.SelectorExpr)

	return ok && isel.Sel.Name == "Status"
}

func hasPlainMergeFrom(call *ast.CallExpr) bool {
	for _, a := range call.Args {
		if c, ok := a.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "MergeFrom" {
				return true
			}
		}
	}

	return false
}
