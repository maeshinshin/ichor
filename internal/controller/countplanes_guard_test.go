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

// countplanesScanDirs are the two packages ADR 0010's appendix found an
// independently-written, cached-client control-plane count in (the
// webhook's admission check and removal.go's own recount) -- the only
// places that bug can resurface. deleteguard.CountApprovedControlPlanes,
// the one canonical count, lives in pkg/deleteguard, outside both, so this
// guard does not need to exempt its own declaration the way
// TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite exempts
// acquireDisruptiveLock in this same package: there is nothing in either
// scanned directory that is allowed to be the real count.
//
// An earlier version of this guard globbed only pkg/deleteguard itself --
// the one package that structurally has exactly one TalosMachineList
// listing site -- which checked nothing a regression could ever trip.
// Scanning the two packages the bug actually lived in, the same
// cross-directory technique dist/chart/chart_test.go already uses for
// "../../config/crd/bases", is what makes this guard able to fail at all.
var countplanesScanDirs = []string{".", "../webhook/v1alpha1"}

// countplanesAllowedFuncs names every function, in either scanned
// directory, allowed to declare a TalosMachineList. Selecting the machines
// of a cluster that count (approved, by role, not being deleted) is
// deleteguard.ApprovedMachines's job and nothing else lists for it. The
// entries left do not select by that rule at all.
// Adding a name here must come with the same kind of justification, in that
// function's own doc comment, not just to silence this test.
var countplanesAllowedFuncs = map[string]bool{
	// machinesOfCluster (talosmachine_controller.go) maps a TalosStorage, TalosConfigPatch or
	// TalosCluster event to every machine of the cluster to re-reconcile -- unapproved and
	// deleting ones included, since both still need their reconcile (the removal finalizer runs
	// on a deleting one). Counting nothing and gating nothing, it cannot use the approved filter.
	"machinesOfCluster": true,
	// knownHosts (discovery.go) collects the addresses of every machine of the cluster so
	// discovery does not probe them. It counts nothing and gates nothing.
	"knownHosts": true,
}

// TestTalosMachineListCountingStaysCentralized parses every non-test
// *.go file under countplanesScanDirs with go/parser and fails if any
// function outside countplanesAllowedFuncs declares a variable typed
// ichorv1alpha1.TalosMachineList. Matched on the type name, not on a call
// to a method named "List" (the technique
// TestAcquireDisruptiveLockIsTheOnlyAcquireCallSite and
// TestFixtureUUIDsAreUnique use for their own, unambiguous shapes): "List"
// itself is an ordinary method both scanned packages call for many
// unrelated kinds of object, so matching on it here would either miss
// real violations hidden behind a differently-shaped call or force every
// legitimate List of some other type into an ever-growing exception list.
//
// If a quorum or removal test ever flakes, suspect the count's read path
// (a cached client vs. a direct one) before the environment: that is
// exactly how the bug this guard exists for was found.
func TestTalosMachineListCountingStaysCentralized(t *testing.T) {
	fset := token.NewFileSet()
	found := 0

	for _, dir := range countplanesScanDirs {
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

			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}

				ast.Inspect(fn.Body, func(n ast.Node) bool {
					spec, ok := n.(*ast.ValueSpec)
					if !ok {
						return true
					}

					sel, ok := spec.Type.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "TalosMachineList" {
						return true
					}

					found++

					if !countplanesAllowedFuncs[fn.Name.Name] {
						where := fset.Position(spec.Pos())
						t.Errorf("%s:%d: %s declares a TalosMachineList to count or select machines by hand; "+
							"a control-plane count must go through deleteguard.CountApprovedControlPlanes -- "+
							"if this is a different, legitimate purpose, add %q to countplanesAllowedFuncs "+
							"with why, in that function's own doc comment",
							filepath.Base(where.Filename), where.Line, fn.Name.Name, fn.Name.Name)
					}

					return true
				})
			}
		}
	}

	if found == 0 {
		t.Fatalf("no TalosMachineList declaration found at all under %v; this guard has nothing to check -- "+
			"did machinesOfCluster get renamed or removed?", countplanesScanDirs)
	}
}
