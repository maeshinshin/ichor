package main

import (
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/packages"
)

// fieldKind is how one field (or a slice/map element, or a pointer target)
// converts between its mirror and machinery representations.
type fieldKind int

const (
	kPassthrough   fieldKind = iota // identical type on both sides
	kCast                           // named basic type (e.g. InstallDiskType); needs a Go type conversion
	kBytesCast                      // named []byte type (e.g. Base64Bytes); needs a Go type conversion
	kStruct                         // a mirrored struct, see structDef
	kPointer                        // *elem
	kSlice                          // []elem
	kMap                            // map[string]elem
	kSubstScalar                    // substitution-table entry: mirror is a string
	kSubstArgValue                  // substitution-table entry: meta.ArgValue, mirror is []string
	kDropped                        // meta.Unstructured, or a container of nothing but dropped fields
)

// typeMapping is a node in the (small, non-recursive-in-practice) tree
// describing how one machinery Go type maps onto the generated mirror type.
type typeMapping struct {
	kind fieldKind

	mirrorExpr string // Go type expression on the mirror side (e.g. "string", "int32")
	machExpr   string // Go type expression on the machinery side, qualified (e.g. "mv1alpha1.InstallDiskType")

	// machNamedCast is set when the machinery-side container itself is a
	// named type (e.g. meta.Args = map[string]ArgValue) rather than a plain
	// slice/map: ToMachinery must cast the assembled plain container into
	// it.
	machNamedCast string

	elem      *typeMapping // for kPointer, kSlice, kMap
	structDef *structDef   // for kStruct
}

// structDef is one mirror struct the generator emits, plus everything the
// conversion codegen needs to rebuild the corresponding machinery struct.
type structDef struct {
	name     string // Go identifier, shared between mirror and machinery sides
	pkgPath  string // machinery package path this struct comes from
	fields   []*fieldDef
	isRoot   bool
	rootKind rootKind
	// pages holds the reference page of every root this struct is reachable from.
	pages map[string]bool
}

type fieldDef struct {
	name     string // Go field name, shared between mirror and machinery sides
	jsonName string
	optional bool
	mapping  *typeMapping
}

type droppedField struct {
	Struct, Field, YAML string
}

type generator struct {
	pkgs      []*packages.Package
	byPath    map[string]*packages.Package
	structs   map[*types.Named]*structDef
	nameOwner map[string]*types.Named // collision detection: mirror struct name -> source type
	order     []*structDef            // emission order, first-seen
	dropped   []droppedField
	subst     map[string]bool // qualified type names substituted, for the report
}

func newGenerator(pkgs []*packages.Package) *generator {
	g := &generator{
		pkgs:      pkgs,
		byPath:    map[string]*packages.Package{},
		structs:   map[*types.Named]*structDef{},
		nameOwner: map[string]*types.Named{},
		subst:     map[string]bool{},
	}

	var walk func(p *packages.Package)
	walk = func(p *packages.Package) {
		if _, ok := g.byPath[p.PkgPath]; ok {
			return
		}

		g.byPath[p.PkgPath] = p

		for _, imp := range p.Imports {
			walk(imp)
		}
	}

	for _, p := range pkgs {
		walk(p)
	}

	return g
}

func (g *generator) lookupType(pkgPath, name string) *types.Named {
	p, ok := g.byPath[pkgPath]
	if !ok {
		fatalf("package %s was not loaded", pkgPath)
	}

	obj := p.Types.Scope().Lookup(name)
	if obj == nil {
		fatalf("type %s not found in %s", name, pkgPath)
	}

	named, ok := obj.Type().(*types.Named)
	if !ok {
		fatalf("%s.%s is not a named type", pkgPath, name)
	}

	return named
}

func qualifiedName(n *types.Named) string {
	pkg := n.Obj().Pkg()
	if pkg == nil {
		return n.Obj().Name()
	}

	return pkg.Path() + "." + n.Obj().Name()
}

// machImportAlias returns the import alias this generator uses for a
// machinery package path, for qualifying generated type expressions.
func machImportAlias(pkgPath string) string {
	switch pkgPath {
	case pkgRuntime:
		return "mruntime"
	case pkgV1Alpha1:
		return "mv1alpha1"
	case pkgMeta:
		return "mmeta"
	case "github.com/siderolabs/crypto/x509":
		return "mx509"
	case "time":
		return "time"
	default:
		fatalf("package %s has no registered import alias; add one in walk.go", pkgPath)
		return ""
	}
}

func (g *generator) qualify(n *types.Named) string {
	pkg := n.Obj().Pkg()
	if pkg == nil {
		return n.Obj().Name()
	}

	return machImportAlias(pkg.Path()) + "." + n.Obj().Name()
}

func isByteSlice(t types.Type) bool {
	sl, ok := t.(*types.Slice)
	if !ok {
		return false
	}

	b, ok := sl.Elem().(*types.Basic)

	return ok && b.Kind() == types.Byte
}

// mapType computes the mirror representation of a machinery Go type.
func (g *generator) mapType(t types.Type) *typeMapping {
	switch tt := t.(type) {
	case *types.Alias:
		return g.mapType(types.Unalias(tt))

	case *types.Pointer:
		elem := g.mapType(tt.Elem())
		if elem.kind == kDropped {
			return elem
		}

		return &typeMapping{kind: kPointer, elem: elem}

	case *types.Slice:
		elem := g.mapType(tt.Elem())
		if elem.kind == kDropped {
			return elem
		}

		return &typeMapping{kind: kSlice, elem: elem}

	case *types.Map:
		if _, ok := tt.Key().(*types.Basic); !ok {
			fatalf("map key type %s is not a plain string; extend generator", tt.Key())
		}

		elem := g.mapType(tt.Elem())
		if elem.kind == kDropped {
			return elem
		}

		return &typeMapping{kind: kMap, elem: elem}

	case *types.Basic:
		return &typeMapping{kind: kPassthrough, mirrorExpr: tt.Name(), machExpr: tt.Name()}

	case *types.Named:
		return g.mapNamed(tt)
	}

	fatalf("unsupported type %s (%T); extend generator", t, t)

	return nil
}

func (g *generator) mapNamed(n *types.Named) *typeMapping {
	qualified := qualifiedName(n)

	if qualified == pkgMeta+".Unstructured" {
		g.subst[qualified] = true

		return &typeMapping{kind: kDropped}
	}

	if entry, ok := substTable[qualified]; ok {
		g.subst[qualified] = true

		kind := kSubstScalar
		if entry == substArgValue {
			kind = kSubstArgValue
		}

		return &typeMapping{kind: kind, mirrorExpr: entry.mirrorExpr(), machExpr: g.qualify(n)}
	}

	switch u := n.Underlying().(type) {
	case *types.Struct:
		return g.structMapping(n, u)

	case *types.Slice:
		if isByteSlice(u) {
			return &typeMapping{kind: kBytesCast, mirrorExpr: "[]byte", machExpr: g.qualify(n)}
		}

		elem := g.mapType(u.Elem())
		if elem.kind == kDropped {
			return elem
		}

		return &typeMapping{kind: kSlice, elem: elem, machNamedCast: g.qualify(n)}

	case *types.Map:
		if _, ok := u.Key().(*types.Basic); !ok {
			fatalf("named map %s has a non-string key; extend generator", qualified)
		}

		elem := g.mapType(u.Elem())
		if elem.kind == kDropped {
			return elem
		}

		return &typeMapping{kind: kMap, elem: elem, machNamedCast: g.qualify(n)}

	case *types.Basic:
		return &typeMapping{kind: kCast, mirrorExpr: u.Name(), machExpr: g.qualify(n)}
	}

	fatalf(
		"%s has no faithful structural-schema representation (underlying %T, no exported fields to mirror); "+
			"add it to substTable in main.go and report it",
		qualified, n.Underlying(),
	)

	return nil
}

func isMetaMeta(t types.Type) bool {
	n, ok := t.(*types.Named)

	return ok && qualifiedName(n) == pkgMeta+".Meta"
}

func (g *generator) structMapping(named *types.Named, st *types.Struct) *typeMapping {
	if def, ok := g.structs[named]; ok {
		return &typeMapping{kind: kStruct, structDef: def}
	}

	name := named.Obj().Name()
	if owner, ok := g.nameOwner[name]; ok && owner != named {
		fatalf("mirror struct name collision on %q between %s and %s", name, qualifiedName(owner), qualifiedName(named))
	}

	g.nameOwner[name] = named

	def := &structDef{name: name, pkgPath: named.Obj().Pkg().Path()}
	g.structs[named] = def // register before recursing, in case of cycles
	g.order = append(g.order, def)

	for i := range st.NumFields() {
		fld := st.Field(i)
		tag := st.Tag(i)

		if fld.Anonymous() {
			if isMetaMeta(fld.Type()) {
				continue // document identity; the root's constructor sets it instead
			}

			fatalf("unexpected anonymous field %s in %s; extend generator", fld.Type(), name)
		}

		yamlName, omitempty, skip := parseYAMLTag(tag, fld.Name())
		if skip {
			continue
		}

		mapping := g.mapType(fld.Type())
		if mapping.kind == kDropped {
			g.dropped = append(g.dropped, droppedField{Struct: name, Field: fld.Name(), YAML: yamlName})

			continue
		}

		def.fields = append(def.fields, &fieldDef{
			name:     fld.Name(),
			jsonName: yamlName,
			optional: omitempty,
			mapping:  mapping,
		})
	}

	return &typeMapping{kind: kStruct, structDef: def}
}

// parseYAMLTag reads the `yaml:"..."` tag machinery puts on every field and
// returns the name json should use, whether it is optional, and whether the
// field should be skipped entirely (yaml:"-").
func parseYAMLTag(tag, fieldName string) (name string, omitempty, skip bool) {
	yamlTag := reflect.StructTag(tag).Get("yaml")
	if yamlTag == "" {
		return lowerFirst(fieldName), false, false
	}

	parts := strings.Split(yamlTag, ",")
	name = parts[0]

	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}

	if name == "-" {
		return "", false, true
	}

	if name == "" {
		name = lowerFirst(fieldName)
	}

	return name, omitempty, false
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}
