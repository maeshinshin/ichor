package main

import (
	"go/types"
	"path"
	"reflect"
	"slices"
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
	kOverride                       // field-override-table entry: meta.Unstructured mirrored as an upstream type
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

	// overridePtr is set for kOverride entries whose upstream type is a
	// struct (not a map): the mirror side is a pointer to it even though
	// machinery's side is always a plain meta.Unstructured value, so that
	// omitempty can tell "unset" apart from the type's zero value (see
	// emitOverrideConvert). Map-shaped overrides (e.g. corev1.ResourceList)
	// don't need this: a nil map already round-trips through
	// meta.Unstructured{Object: nil} and omitempty on its own.
	overridePtr bool

	elem      *typeMapping // for kPointer, kSlice, kMap
	structDef *structDef   // for kStruct
}

// structDef is one mirror struct the generator emits, plus everything the
// conversion codegen needs to rebuild the corresponding machinery struct.
type structDef struct {
	name string // mirror-side Go identifier
	// machName is the machinery-side identifier, normally the same as name;
	// it differs only for the handful of typeRenames entries (main.go),
	// where name collides with an unrelated existing mirror struct and had
	// to change on the mirror side while the machinery type obviously can't
	// be renamed to match.
	machName string
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

// droppedSecretField is a secretFields entry actually hit while walking, for
// the report. It carries Why (what the field holds) rather than reusing
// droppedField, so the report can say *why* in a way that reads differently
// from "can't represent this" -- see secretFields' doc comment in main.go.
// Scalar records the dropped field's own shape (string/bool/number vs.
// struct) so a CRD-schema check matching by YAML name can also require the
// shape to match: two unrelated fields can legitimately share a YAML name
// at different schema depths (RegistryConfig.auth, an object, vs.
// RegistryAuthConfig.RegistryAuth, a dropped string, both tagged "auth"),
// and shape is what tells them apart without tracking full paths.
type droppedSecretField struct {
	Struct, Field, YAML, Why string
	Scalar                   bool
}

type generator struct {
	pkgs          []*packages.Package
	byPath        map[string]*packages.Package
	structs       map[*types.Named]*structDef
	nameOwner     map[string]*types.Named // collision detection: mirror struct name -> source type
	order         []*structDef            // emission order, first-seen
	dropped       []droppedField
	droppedSecret []droppedSecretField
	subst         map[string]bool // qualified type names substituted, for the report
	overridden    []string        // "Struct.Field" entries from fieldOverrides actually used, for the report
	roots         []rootKind      // the shipped kinds, in union field order
	notes         []string        // shipped kinds no longer classed accept, for the report
	pending       []string        // accept kinds not shipped yet, for the report
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

// importsUsed is every machinery package machImportAlias handed out an alias for, alias to path: the import
// block of zz_generated.conversion.go. An alias is either the one written in the switch (a name that other
// code and the hand-written helpers already use) or "m" ("r" under resources/) plus the package's last path element.
var importsUsed = map[string]string{}

// machImportAlias returns the import alias this generator uses for a
// machinery package path, for qualifying generated type expressions.
func machImportAlias(pkgPath string) string {
	alias := ""

	switch pkgPath {
	case pkgRuntime:
		alias = "mruntime"
	case pkgV1Alpha1:
		alias = "mv1alpha1"
	case pkgMeta:
		alias = "mmeta"
	case "github.com/siderolabs/crypto/x509":
		alias = "mx509"
	case "time":
		return "time"
	case "net/netip":
		alias = "netip"
	case pkgCoreV1:
		alias = "corev1"
	case pkgSpecs:
		alias = "specs"
	case pkgAuditV1:
		alias = "auditv1"
	case pkgAPIServerV1:
		alias = "apiserverv1"
	case pkgKubeletV1:
		alias = "kubeletv1"
	case pkgBlock:
		alias = "mblock"
	case pkgNethelpers:
		alias = "mnethelpers"
	case pkgNetwork:
		alias = "mnetwork"
	case pkgResourcesBlock:
		alias = "rblock"
	case pkgCel:
		alias = "mcel"
	default:
		// Talos's resources tree repeats the names of its config/types tree (block, storage), so the two
		// trees take different prefixes.
		alias = "m" + path.Base(pkgPath)
		if strings.Contains(pkgPath, "/machinery/resources/") {
			alias = "r" + path.Base(pkgPath)
		}
	}

	if other, ok := importsUsed[alias]; ok && other != pkgPath {
		fatalf("import alias %s is taken by %s and wanted by %s; add a case for one of them in walk.go", alias, other, pkgPath)
	}

	importsUsed[alias] = pkgPath

	return alias
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

// isScalarType reports whether t renders as a JSON scalar (string, bool,
// number) rather than an object or array -- used to tell a dropped
// secretFields entry's own shape, so a CRD-schema check can require a
// candidate match to have the same shape, not just the same YAML name.
func isScalarType(t types.Type) bool {
	for {
		switch tt := t.(type) {
		case *types.Alias:
			t = types.Unalias(tt)
		case *types.Pointer:
			t = tt.Elem()
		case *types.Named:
			t = tt.Underlying()
		case *types.Basic:
			return true
		default:
			return false // struct, slice, map: object- or array-shaped
		}
	}
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

// overrideMapping builds the typeMapping for a fieldOverrides entry: the
// field's machinery type is meta.Unstructured (every entry in that table is,
// by construction -- see main.go), and the mirror type is the upstream type
// named there instead, converted through the generic JSON helpers in
// substitution.go rather than recursing into it.
func (g *generator) overrideMapping(fieldKey string, ov fieldOverride) *typeMapping {
	named := g.lookupType(ov.pkgPath, ov.typeName)
	g.overridden = append(g.overridden, fieldKey)

	_, isMap := named.Underlying().(*types.Map)

	mirrorExpr := ov.localWrapper
	if mirrorExpr == "" {
		mirrorExpr = g.qualify(named)
	}

	return &typeMapping{
		kind:        kOverride,
		mirrorExpr:  mirrorExpr,
		machExpr:    machImportAlias(pkgMeta) + ".Unstructured",
		overridePtr: !isMap,
	}
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
	if renamed, ok := typeRenames[qualifiedName(named)]; ok {
		name = renamed
	}

	if owner, ok := g.nameOwner[name]; ok && owner != named {
		fatalf("mirror struct name collision on %q between %s and %s", name, qualifiedName(owner), qualifiedName(named))
	}

	g.nameOwner[name] = named

	def := &structDef{name: name, machName: named.Obj().Name(), pkgPath: named.Obj().Pkg().Path()}
	g.structs[named] = def // register before recursing, in case of cycles
	g.order = append(g.order, def)

	g.addFields(def, named, st)

	return &typeMapping{kind: kStruct, structDef: def}
}

// addFields appends the fields of st, a struct declared as named, to def. A struct embedded with
// `yaml:",inline"` has its fields written at the top level of the embedding document, so they are
// flattened into def (the CRD then has the shape of the Talos document) and promoted back on conversion:
// the generated code names them as out.LinkUp, which Go resolves through the embedded value. Each field
// is looked up in secretFields, fieldOverrides and sparseFields under the struct that declares it.
func (g *generator) addFields(def *structDef, named *types.Named, st *types.Struct) {
	for i := range st.NumFields() {
		fld := st.Field(i)
		tag := st.Tag(i)

		if fld.Anonymous() {
			if isMetaMeta(fld.Type()) {
				continue // document identity; the root's constructor sets it instead
			}

			if !yamlInline(tag) {
				fatalf("anonymous field %s in %s is not inlined (no yaml inline option); extend generator", fld.Type(), def.name)
			}

			emb, _ := fld.Type().(*types.Named) // a pointer embed is nil until set, so conversion could not promote through it
			if emb == nil {
				fatalf("inlined field %s in %s is not a struct value; extend generator", fld.Type(), def.name)
			}

			est, ok := emb.Underlying().(*types.Struct)
			if !ok {
				fatalf("inlined field %s in %s is not a struct; extend generator", fld.Type(), def.name)
			}

			g.addFields(def, emb, est)

			continue
		}

		yamlName, omitempty, skip := parseYAMLTag(tag, fld.Name())
		if skip {
			continue
		}

		if !fld.Exported() {
			fatalf("%s has the unexported field %s, so it has no structural representation; add its type to substTable in main.go", named.Obj().Name(), fld.Name())
		}

		// Keyed on named.Obj().Name() -- the real machinery type name --
		// not the possibly-renamed mirror name in "def.name": secretFields and
		// fieldOverrides are both written against machinery's own type
		// names (that's what a contributor reads when deciding whether a
		// field needs a row), and typeRenames exists precisely because a
		// mirror name can differ from it. Keying on the mirror name silently
		// stops matching the moment a type gets renamed -- which is
		// exactly what happened to block.EncryptionKey.KeyStatic (renamed
		// to VolumeEncryptionKey) until this was fixed.
		fieldKey := named.Obj().Name() + "." + fld.Name()

		if why, ok := secretFields[fieldKey]; ok {
			g.droppedSecret = append(g.droppedSecret, droppedSecretField{
				Struct: def.name, Field: fld.Name(), YAML: yamlName, Why: why,
				Scalar: isScalarType(fld.Type()),
			})

			continue
		}

		var mapping *typeMapping
		if ov, ok := fieldOverrides[fieldKey]; ok {
			mapping = g.overrideMapping(fieldKey, ov)
		} else {
			mapping = g.mapType(fld.Type())
		}

		if mapping.kind == kDropped {
			g.dropped = append(g.dropped, droppedField{Struct: def.name, Field: fld.Name(), YAML: yamlName})

			continue
		}

		for _, f := range def.fields {
			if f.name == fld.Name() || f.jsonName == yamlName {
				fatalf("field %s (yaml %q) of %s collides with %s (yaml %q) in %s", fld.Name(), yamlName, named.Obj().Name(), f.name, f.jsonName, def.name)
			}
		}

		def.fields = append(def.fields, &fieldDef{
			name:     fld.Name(),
			jsonName: yamlName,
			optional: omitempty || sparseFields[fieldKey],
			mapping:  mapping,
		})
	}
}

// yamlInline reports whether a field tag carries the yaml "inline" option.
func yamlInline(tag string) bool {
	_, opts, _ := strings.Cut(reflect.StructTag(tag).Get("yaml"), ",")

	return slices.Contains(strings.Split(opts, ","), "inline")
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
