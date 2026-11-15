package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// Where the generator reads from, relative to pkg/typedconfig (the directory go generate runs it in).
const (
	mirrorFile = "zz_generated.mirror.go"
	unionFile  = "../../api/v1alpha1/zz_generated.talosconfigpatchdocument.go"
	unionType  = "TalosConfigPatchDocument"
	keyFunc    = "TalosConfigPatchDocumentFieldKey"
	mirrorPkg  = "typedconfig"
)

// tkind is the shape of one type expression in a mirror field.
type tkind int

const (
	tBasic  tkind = iota // string, bool, an integer or a float type
	tStruct              // a struct of the mirror, by name
	tOpaque              // anything else: an imported type, or a hand-written one of the package
	tPtr
	tSlice
	tMap
)

// typ is a mirror field's type, parsed from its source so that it can be written again with the struct names
// of the Ichor schema in place of the mirror's.
type typ struct {
	kind tkind
	name string // tBasic: the Go type; tStruct: the mirror struct; tOpaque: the expression as the mirror writes it
	elem *typ   // tPtr, tSlice, tMap

	// child is the Ichor node a tStruct stands for, set when a type is placed into the tree.
	child *node
}

// srcField is one field of a mirror struct.
type srcField struct {
	goName   string
	json     string
	required bool
	t        *typ
}

// srcStruct is one struct of the generated mirror.
type srcStruct struct {
	name   string
	fields []*srcField
}

// source is what the generator reads: the mirror's structs, the kind of each document root, and the imports
// the opaque types need.
type source struct {
	structs map[string]*srcStruct
	// roots maps a Talos kind to the mirror struct that holds it. "Config" is the v1alpha1 document.
	roots map[string]string
	// kinds lists the kinds in the order the union writes them.
	kinds   []string
	imports map[string]string // import path by the name the mirror uses for it
	opaque  map[string]bool   // names of same-package types that are not structs of the mirror file
}

// loadSource parses the mirror and the union generated beside it.
func loadSource() (*source, error) {
	fset := token.NewFileSet()

	mf, err := parser.ParseFile(fset, mirrorFile, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing the mirror: %w", err)
	}

	s := &source{structs: map[string]*srcStruct{}, roots: map[string]string{}, imports: map[string]string{}, opaque: map[string]bool{}}

	for _, im := range mf.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := im.Name.Name

		s.imports[name] = p
	}

	for _, d := range mf.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}

		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if _, ok := ts.Type.(*ast.StructType); ok {
				s.structs[ts.Name.Name] = &srcStruct{name: ts.Name.Name}
			}
		}
	}

	for _, d := range mf.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}

		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok {
				continue
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}

			if err := s.readStruct(s.structs[ts.Name.Name], st); err != nil {
				return nil, err
			}
		}
	}

	if err := s.readUnion(fset); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *source) readStruct(out *srcStruct, st *ast.StructType) error {
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 && isTypeMeta(f.Type) {
			// The one embedded field of the mirror: apiVersion and kind, written out.
			for _, n := range []string{"apiVersion", "kind"} {
				out.fields = append(out.fields, &srcField{goName: strings.ToUpper(n[:1]) + n[1:], json: n, t: &typ{kind: tBasic, name: "string"}})
			}

			continue
		}

		if len(f.Names) != 1 || f.Tag == nil {
			return fmt.Errorf("%s: a field without one name and a tag", out.name)
		}

		tag, _ := strconv.Unquote(f.Tag.Value)
		name := jsonName(tag)

		if name == "" {
			return fmt.Errorf("%s.%s: no json name", out.name, f.Names[0].Name)
		}

		t, err := s.parseType(f.Type)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", out.name, f.Names[0].Name, err)
		}

		sf := &srcField{goName: f.Names[0].Name, json: name, t: t}

		if f.Doc != nil {
			for _, c := range f.Doc.List {
				if strings.TrimSpace(strings.TrimPrefix(c.Text, "//")) == "+required" {
					sf.required = true
				}
			}
		}

		out.fields = append(out.fields, sf)
	}

	return nil
}

func isTypeMeta(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)

	return ok && sel.Sel.Name == "TypeMeta"
}

// jsonName returns the name a struct tag gives the json key, "" when there is none.
func jsonName(tag string) string {
	const key = `json:"`

	i := strings.Index(tag, key)
	if i < 0 {
		return ""
	}

	rest := tag[i+len(key):]

	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}

	return strings.Split(rest[:j], ",")[0]
}

var basics = map[string]bool{
	"string": true, "bool": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "float32": true, "float64": true,
	"byte": true,
}

func (s *source) parseType(e ast.Expr) (*typ, error) {
	switch t := e.(type) {
	case *ast.Ident:
		switch {
		case basics[t.Name]:
			return &typ{kind: tBasic, name: t.Name}, nil
		case s.structs[t.Name] != nil:
			return &typ{kind: tStruct, name: t.Name}, nil
		default:
			// A type of the package that is not a struct of the mirror file (the hand-written ones).
			s.opaque[t.Name] = true

			return &typ{kind: tOpaque, name: mirrorPkg + "." + t.Name}, nil
		}
	case *ast.StarExpr:
		el, err := s.parseType(t.X)
		if err != nil {
			return nil, err
		}

		return &typ{kind: tPtr, elem: el}, nil
	case *ast.ArrayType:
		if t.Len != nil {
			return nil, fmt.Errorf("an array type")
		}

		el, err := s.parseType(t.Elt)
		if err != nil {
			return nil, err
		}

		return &typ{kind: tSlice, elem: el}, nil
	case *ast.MapType:
		if k, ok := t.Key.(*ast.Ident); !ok || k.Name != "string" {
			return nil, fmt.Errorf("a map that is not keyed by string")
		}

		el, err := s.parseType(t.Value)
		if err != nil {
			return nil, err
		}

		return &typ{kind: tMap, elem: el}, nil
	case *ast.SelectorExpr:
		x, ok := t.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("a selector on a non-name")
		}

		if _, ok := s.imports[x.Name]; !ok {
			return nil, fmt.Errorf("package %s is not imported by the mirror", x.Name)
		}

		return &typ{kind: tOpaque, name: x.Name + "." + t.Sel.Name}, nil
	}

	return nil, fmt.Errorf("a type expression of kind %T", e)
}

// readUnion fills roots and kinds from the union the mirror generator wrote beside the mirror: the struct
// gives each document field its mirror type, and the key function gives each kind its field.
func (s *source) readUnion(fset *token.FileSet) error {
	f, err := parser.ParseFile(fset, unionFile, nil, 0)
	if err != nil {
		return fmt.Errorf("parsing the union: %w", err)
	}

	byKey := map[string]string{} // json key -> mirror struct

	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}

		for _, sp := range gd.Specs {
			ts, ok := sp.(*ast.TypeSpec)
			if !ok || ts.Name.Name != unionType {
				continue
			}

			for _, fl := range ts.Type.(*ast.StructType).Fields.List {
				star, ok := fl.Type.(*ast.StarExpr)
				if !ok || fl.Tag == nil {
					continue
				}

				sel, ok := star.X.(*ast.SelectorExpr)
				if !ok {
					continue
				}

				tag, _ := strconv.Unquote(fl.Tag.Value)
				byKey[jsonName(tag)] = sel.Sel.Name
			}
		}
	}

	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != keyFunc {
			continue
		}

		ast.Inspect(fd.Body, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok || len(cc.List) != 1 || len(cc.Body) != 1 {
				return true
			}

			kl, ok := cc.List[0].(*ast.BasicLit)
			if !ok {
				return true
			}

			ret, ok := cc.Body[0].(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 2 {
				return true
			}

			vl, ok := ret.Results[0].(*ast.BasicLit)
			if !ok {
				return true
			}

			kind, _ := strconv.Unquote(kl.Value)
			key, _ := strconv.Unquote(vl.Value)

			s.roots[kind] = byKey[key]
			s.kinds = append(s.kinds, kind)

			return true
		})
	}

	if len(s.kinds) == 0 {
		return fmt.Errorf("no kinds read from %s", keyFunc)
	}

	for k, st := range s.roots {
		if s.structs[st] == nil {
			return fmt.Errorf("kind %s: the union names %q, which is not a struct of the mirror", k, st)
		}
	}

	return nil
}
