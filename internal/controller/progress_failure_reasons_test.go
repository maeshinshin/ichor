package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// notFailures are the reason constants of the upgrade files that are not a failure of a progress condition.
var notFailures = map[string]bool{
	"reasonUpgradeRequested": true, "reasonUpgradeConverged": true, "reasonUpgradeVerifying": true, "reasonKubernetesApplied": true,
	"reasonKubernetesConverged": true, "reasonRemovedAPIsInUse": true, "reasonRemovedAPIsNone": true,
	"reasonRemovedAPIsUnknown": true,
}

// TestProgressFailureReasonsCoverUpgradeReasons fails when an upgrade file gains a reason constant that is
// neither in ProgressFailureReasons nor listed in notFailures, or when the set holds a value no constant has.
func TestProgressFailureReasonsCoverUpgradeReasons(t *testing.T) {
	values := map[string]bool{}

	for _, file := range []string{"talos_upgrade.go", "kubernetes_upgrade.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}

			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "reason") {
						continue
					}

					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok {
						continue
					}

					v, _ := strconv.Unquote(lit.Value)
					if notFailures[name.Name] {
						continue
					}

					values[v] = true

					if !ProgressFailureReasons[v] {
						t.Errorf("%s = %q is not in ProgressFailureReasons or notFailures", name.Name, v)
					}
				}
			}
		}
	}

	for v := range ProgressFailureReasons {
		if !values[v] {
			t.Errorf("ProgressFailureReasons holds %q, which no upgrade reason constant has", v)
		}
	}
}
