// Package rolloutcheck holds the check that every use of cilium.DaemonSetRolledOut declares which question it asks.
//
// DaemonSetRolledOut answers "is Cilium healthy?": an OnDelete DaemonSet with old pods counts as rolled out. A caller
// that has just changed what the agents read at start (the kube-proxy mode, the image) must ask
// DaemonSetRunsCurrentTemplate instead, or it reports the change done while no agent restarted. A new caller of the
// lenient predicate fails here until it is listed with its reason.
package rolloutcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// healthCallers are the functions that ask the health question, keyed by "path:function".
var healthCallers = map[string]string{
	"pkg/cni/cilium/health.go:CheckAgentsReady":                 "the adopt preflight: taking over restarts the agents, so it needs a healthy Cilium, not a rolled-out change",
	"pkg/cni/cilium/health.go:DaemonSetRunsCurrentTemplate":     "the strict predicate is the health predicate plus the updated count",
	"internal/controller/cnihandover_controller.go:ciliumReady": "the hand-over's wait for a bootstrapped Cilium before its first Job: no change has been made yet",
}

func TestEveryRolloutCheckDeclaresItsQuestion(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	seen := map[string]bool{}

	for _, dir := range []string{"internal", "pkg", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}

			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)

			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}

			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Name.Name == "DaemonSetRolledOut" {
					continue
				}

				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if name := referenced(n); name == "DaemonSetRolledOut" {
						key := rel + ":" + fn.Name.Name
						seen[key] = true

						if _, ok := healthCallers[key]; !ok {
							t.Errorf("%s uses DaemonSetRolledOut (is Cilium healthy?) and is not in healthCallers: if it "+
								"has just changed what the agents read at start, call DaemonSetRunsCurrentTemplate; "+
								"if it asks about health, list it with its reason", key)
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

	for key := range healthCallers {
		if !seen[key] {
			t.Errorf("healthCallers lists %s but it no longer uses DaemonSetRolledOut: remove it", key)
		}
	}
}

// referenced is the name n refers to by f or pkg.f: a call, or the function passed as a value.
func referenced(n ast.Node) string {
	switch f := n.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}

	return ""
}
