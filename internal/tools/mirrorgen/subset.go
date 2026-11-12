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
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// A subset override mirrors an upstream struct as a local struct without the fields its omits table names.
// The kept fields keep the upstream Go type, JSON name and schema markers, so the CRD describes them exactly
// as upstream does; a nested struct that has omits rows of its own becomes a local struct too. Every field of
// the upstream struct is therefore either kept or named in omits, and a field a later upstream release adds
// is kept until someone reads it, which stops generation if the CRD schema cannot express it.

// subsetStruct is one local struct of a subset override.
type subsetStruct struct {
	name     string
	upstream *types.Named
	fields   []subsetField
	omitted  []string // JSON names, sorted
}

type subsetField struct {
	goName, typeExpr, jsonName string
	markers                    []string
}

// subsetState is what the generator accumulates while building subsets.
type subsetState struct {
	byType  map[*types.Named]*subsetStruct
	order   []*subsetStruct
	imports map[string]string // alias -> import path
	hit     map[string]bool   // omits rows that named a real field
	refused map[string]bool   // refusedFields rows that matched a field
	files   map[string]*ast.File
	fset    *token.FileSet
	// problems found while building, reported by omissionProblems.
	problems []string
}

func (g *generator) subsetState() *subsetState {
	if g.subset == nil {
		g.subset = &subsetState{
			byType: map[*types.Named]*subsetStruct{}, imports: map[string]string{},
			hit: map[string]bool{}, refused: map[string]bool{}, files: map[string]*ast.File{}, fset: token.NewFileSet(),
		}
	}

	return g.subset
}

// subsetAliases are the import aliases the local structs use for upstream packages.
var subsetAliases = map[string]string{
	pkgCoreV1:                               aliasCoreV1,
	pkgKubeletV1Beta1:                       "kubeletv1beta1",
	pkgKubeProxyV1Alpha1:                    "kubeproxyv1alpha1",
	pkgKubeSchedulerV1:                      "kubeschedulerv1",
	"k8s.io/apimachinery/pkg/apis/meta/v1":  "metav1",
	"k8s.io/apimachinery/pkg/api/resource":  "resource",
	"k8s.io/apimachinery/pkg/util/intstr":   "intstr",
	"k8s.io/component-base/tracing/api/v1":  "tracingv1",
	"k8s.io/component-base/config/v1alpha1": "componentbasev1alpha1",
}

func (g *generator) subsetQualifier(p *types.Package) string {
	alias, ok := subsetAliases[p.Path()]
	if !ok {
		fatalf("subset field type from %s: add an import alias for it to subsetAliases in subset.go", p.Path())
	}

	g.subsetState().imports[alias] = p.Path()

	return alias
}

// subsetFor returns the local struct for upstream, building it (and the local structs of nested types that have
// omits rows) on first use. Rows are keyed "UpstreamStruct.jsonName".
func (g *generator) subsetFor(upstream *types.Named, omits map[string]string) *subsetStruct {
	st := g.subsetState()
	if s, ok := st.byType[upstream]; ok {
		return s
	}

	str, ok := upstream.Underlying().(*types.Struct)
	if !ok {
		fatalf("subset root %s is not a struct", qualifiedName(upstream))
	}

	s := &subsetStruct{name: upstream.Obj().Name() + "Subset", upstream: upstream}
	if owner, ok := g.nameOwner[s.name]; ok && owner != upstream {
		fatalf("mirror struct name collision on %q between %s and %s", s.name, qualifiedName(owner), qualifiedName(upstream))
	}

	g.nameOwner[s.name] = upstream
	st.byType[upstream] = s // before recursing, in case of cycles
	st.order = append(st.order, s)

	for i := range str.NumFields() {
		fld := str.Field(i)
		jsonName, skip := jsonTagName(str.Tag(i))

		switch {
		case fld.Anonymous() && isTypeMeta(fld.Type()):
			continue // apiVersion and kind: Talos sets them
		case fld.Anonymous():
			// An embedded struct is named by its type, and a subset only omits it.
			key := upstream.Obj().Name() + "." + fld.Name()
			if _, ok := omits[key]; !ok {
				st.problems = append(st.problems, fmt.Sprintf("%s embeds %s, which a subset does not flatten: add the omits row %q", s.name, fld.Type(), key))

				continue
			}

			st.hit[key] = true
			s.omitted = append(s.omitted, fld.Name())

			continue
		case skip || !fld.Exported():
			continue
		}

		key := upstream.Obj().Name() + "." + jsonName
		if _, ok := omits[key]; ok {
			st.hit[key] = true
			s.omitted = append(s.omitted, jsonName)

			continue
		}

		expr := g.subsetTypeExpr(fld.Type(), omits)
		if isStructValue(fld.Type()) {
			expr = "*" + expr // omitempty does not omit a struct value: an unset one would write its zero value
		}

		s.fields = append(s.fields, subsetField{
			goName: fld.Name(), jsonName: jsonName, typeExpr: expr, markers: g.schemaMarkers(fld),
		})
	}

	slices.Sort(s.omitted)

	return s
}

// subsetTypeExpr is the Go type expression of an upstream field type in the local struct.
func (g *generator) subsetTypeExpr(t types.Type, omits map[string]string) string {
	switch tt := t.(type) {
	case *types.Alias:
		return g.subsetTypeExpr(types.Unalias(tt), omits)
	case *types.Pointer:
		return "*" + g.subsetTypeExpr(tt.Elem(), omits)
	case *types.Slice:
		return "[]" + g.subsetTypeExpr(tt.Elem(), omits)
	case *types.Map:
		return "map[" + g.subsetTypeExpr(tt.Key(), omits) + "]" + g.subsetTypeExpr(tt.Elem(), omits)
	case *types.Named:
		if _, isStruct := tt.Underlying().(*types.Struct); isStruct && hasOmitRows(omits, tt.Obj().Name()) {
			return g.subsetFor(tt, omits).name
		}
	}

	return types.TypeString(t, g.subsetQualifier)
}

func isStructValue(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}

	_, isStruct := n.Underlying().(*types.Struct)

	return isStruct
}

func hasOmitRows(omits map[string]string, structName string) bool {
	for k := range omits {
		if strings.HasPrefix(k, structName+".") {
			return true
		}
	}

	return false
}

func isTypeMeta(t types.Type) bool {
	n, ok := t.(*types.Named)

	return ok && n.Obj().Name() == "TypeMeta" && n.Obj().Pkg().Path() == "k8s.io/apimachinery/pkg/apis/meta/v1"
}

// jsonTagName is the name in a field's json tag, and whether the field is skipped (json:"-").
func jsonTagName(tag string) (name string, skip bool) {
	name, _, _ = strings.Cut(reflect.StructTag(tag).Get("json"), ",")

	return name, name == "-"
}

// schemaMarkers returns the kubebuilder marker comments on an upstream field, read from the source file the
// field is declared in. The list type and map key markers are not copied: a map-typed list needs its key to
// have a default, which the upstream types do not give, and an atomic list loses nothing a patch needs.
func (g *generator) schemaMarkers(fld *types.Var) []string {
	st := g.subsetState()
	pos := g.pkgs[0].Fset.Position(fld.Pos())

	f, ok := st.files[pos.Filename]
	if !ok {
		var err error

		if f, err = parser.ParseFile(st.fset, pos.Filename, nil, parser.ParseComments); err != nil {
			fatalf("reading %s for the markers of %s: %v", pos.Filename, fld.Name(), err)
		}

		st.files[pos.Filename] = f
	}

	var out []string

	ast.Inspect(f, func(n ast.Node) bool {
		af, ok := n.(*ast.Field)
		if !ok || af.Doc == nil || len(af.Names) == 0 || st.fset.Position(af.Names[0].Pos()).Line != pos.Line {
			return true
		}

		for _, c := range af.Doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if strings.HasPrefix(text, "+kubebuilder:") {
				out = append(out, text)
			}
		}

		return false
	})

	return out
}

// omissionProblems lists what is wrong with the omission tables: an omits row naming no upstream field, a
// subset that omits nothing, and a refusedFields row that matched no field of a shipped kind.
func (g *generator) omissionProblems(overrides map[string]fieldOverride, refused map[string]string) []string {
	st := g.subsetState()

	var problems []string

	problems = append(problems, st.problems...)

	for _, ov := range overrides {
		for k := range ov.omits {
			if !st.hit[k] {
				problems = append(problems, fmt.Sprintf("fieldOverrides omits row %q names no field of a subset: remove it", k))
			}
		}
	}

	for _, s := range st.order {
		if len(s.omitted) == 0 {
			problems = append(problems, fmt.Sprintf("subset %s omits no field: use a plain override", s.name))
		}
	}

	for _, k := range slices.Sorted(maps.Keys(refused)) {
		if !st.refused[k] {
			problems = append(problems, fmt.Sprintf("refusedFields row %q matches no field of a shipped kind: remove it", k))
		}
	}

	return slices.Compact(slices.Sorted(slices.Values(problems)))
}

// writeSubsets appends the local structs, sorted by name.
func (g *generator) writeSubsets(b *strings.Builder) {
	order := slices.Clone(g.subsetState().order)
	slices.SortFunc(order, func(a, b *subsetStruct) int { return strings.Compare(a.name, b.name) })

	for _, s := range order {
		fmt.Fprintf(b, "\n// %s mirrors %s.%s. Ichor does not carry %s.\n", s.name, s.upstream.Obj().Pkg().Path(),
			s.upstream.Obj().Name(), strings.Join(s.omitted, ", "))
		fmt.Fprintf(b, "type %s struct {\n", s.name)

		for _, f := range s.fields {
			b.WriteString("\t// +optional\n")

			for _, m := range f.markers {
				fmt.Fprintf(b, "\t// %s\n", m)
			}

			fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", f.goName, f.typeExpr, f.jsonName)
		}

		b.WriteString("}\n")
	}
}

// mirrorImports is the import block of the mirror file: the packages the walked structs name, plus the
// upstream packages the local structs name.
func (g *generator) mirrorImports() string {
	imports := map[string]string{ // path -> alias
		pkgCoreV1: aliasCoreV1, pkgAPIServerV1: "apiserverv1", pkgAuditV1: "auditv1", pkgKubeletV1: "kubeletv1",
	}

	for alias, p := range g.subsetState().imports {
		imports[p] = alias
	}

	var b strings.Builder

	for _, p := range slices.SortedFunc(maps.Keys(imports), func(a, b string) int { return strings.Compare(a, b) }) {
		fmt.Fprintf(&b, "\t%s %q\n", imports[p], p)
	}

	return b.String()
}
