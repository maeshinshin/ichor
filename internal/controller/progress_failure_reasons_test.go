package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"regexp"
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

// TestUIProgressFailureReasonsMatchController keeps the list in ui/src/lib/conditions.ts equal to
// ProgressFailureReasons, so the UI and the CLI cannot disagree on what a failed upgrade is.
func TestUIProgressFailureReasonsMatchController(t *testing.T) {
	src, err := os.ReadFile("../../ui/src/lib/conditions.ts")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`const progressFailureReasons = \[([^\]]*)\]`).FindSubmatch(src)
	if m == nil {
		t.Fatal("progressFailureReasons not found in conditions.ts")
	}

	ui := map[string]bool{}
	for _, r := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(m[1], -1) {
		ui[string(r[1])] = true
	}

	if !maps.Equal(ui, ProgressFailureReasons) {
		t.Errorf("conditions.ts progressFailureReasons = %v, controller.ProgressFailureReasons = %v", ui, ProgressFailureReasons)
	}
}
