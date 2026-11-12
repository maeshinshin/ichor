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

package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	pkgTypesTree   = "github.com/siderolabs/talos/pkg/machinery/config/types/..."
	pkgTalosRegist = "github.com/siderolabs/talos/pkg/machinery/config/internal/registry"
)

// registered is what one registry.Register call in Talos's config/types tree says about a document kind.
type registered struct {
	kind string
	// pkgPath/typeName is the document struct the call's factory returns for its (single) version.
	pkgPath  string
	typeName string
	// constPkg/constName name the kind constant the call passes; empty when the kind is a string literal
	// (the legacy v1alpha1 document), which has no Meta to set.
	constPkg  string
	constName string
	// apiVersion is the version the factory switches on; empty when it does not switch (legacy v1alpha1).
	apiVersion string
	// ambiguous: the factory returns more than one type or version; deriveRoots refuses it for a shipped kind.
	ambiguous bool
}

// scanRegistry returns every registry.Register call in the config/types packages among pkgs, keyed by kind.
// The registry package is internal to machinery, so its Kinds() cannot be imported; the init() calls that
// fill it are read instead, with the kind constant resolved by the type checker.
func scanRegistry(pkgs []*packages.Package) (map[string]registered, error) {
	out := map[string]registered{}

	for _, p := range pkgs {
		if !strings.HasPrefix(p.PkgPath, strings.TrimSuffix(pkgTypesTree, "...")) {
			continue
		}

		for _, f := range p.Syntax {
			var err error

			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 || err != nil {
					return true
				}

				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}

				fn, ok := p.TypesInfo.Uses[sel.Sel].(*types.Func)
				if !ok || fn.Name() != "Register" || fn.Pkg() == nil || fn.Pkg().Path() != pkgTalosRegist {
					return true
				}

				var r registered

				r, err = registeredFrom(p, call)
				if err == nil {
					out[r.kind] = r
				}

				return true
			})

			if err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

func registeredFrom(p *packages.Package, call *ast.CallExpr) (registered, error) {
	at := p.Fset.Position(call.Pos())

	v := p.TypesInfo.Types[call.Args[0]].Value
	if v == nil || v.Kind() != constant.String {
		return registered{}, fmt.Errorf("%s: registry.Register with a kind that is not a constant string", at)
	}

	r := registered{kind: constant.StringVal(v)}

	switch a := call.Args[0].(type) {
	case *ast.Ident:
		if c, ok := p.TypesInfo.Uses[a].(*types.Const); ok {
			r.constPkg, r.constName = c.Pkg().Path(), c.Name()
		}
	case *ast.SelectorExpr:
		if c, ok := p.TypesInfo.Uses[a.Sel].(*types.Const); ok {
			r.constPkg, r.constName = c.Pkg().Path(), c.Name()
		}
	}

	fl, ok := call.Args[1].(*ast.FuncLit)
	if !ok {
		return registered{}, fmt.Errorf("%s: registry.Register(%q) with a factory that is not a function literal", at, r.kind)
	}

	// The factory is `switch version { case "v1alpha1": return &T{} ... }`, or a bare `return &T{}`.
	var version string

	ast.Inspect(fl.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.CaseClause:
			version = ""
			if len(s.List) == 1 {
				if cv := p.TypesInfo.Types[s.List[0]].Value; cv != nil && cv.Kind() == constant.String {
					version = constant.StringVal(cv)
				}
			}
		case *ast.ReturnStmt:
			if len(s.Results) != 1 {
				return true
			}

			u, ok := s.Results[0].(*ast.UnaryExpr)
			if !ok {
				return true
			}

			named, ok := p.TypesInfo.TypeOf(u.X).(*types.Named)
			if !ok {
				return true
			}

			if r.typeName != "" {
				r.ambiguous = true

				return true
			}

			r.pkgPath, r.typeName, r.apiVersion = named.Obj().Pkg().Path(), named.Obj().Name(), version
		}

		return true
	})

	if r.typeName == "" {
		return registered{}, fmt.Errorf("%s: no document type found in the factory of kind %q", at, r.kind)
	}

	return r, nil
}
