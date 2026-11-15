package main

import (
	"fmt"
	"slices"
	"strings"
)

// sink says where one setting of Ichor's schema goes in a Talos document: the kind of the document ("Config"
// is the v1alpha1 document) and the dotted JSON path inside it. The path of a subtree is the path of its root;
// nothing below it needs a sink of its own when the subtree is the mirror's own struct.
type sink struct {
	kind, path string
}

func (s sink) String() string {
	if s.path == "" {
		return s.kind + ":"
	}

	return s.kind + ":" + s.path
}

func (s sink) join(name string) sink {
	if s.path == "" {
		return sink{s.kind, name}
	}

	return sink{s.kind, s.path + "." + name}
}

// node is one object of Ichor's schema. It starts as a reference to a mirror struct, which is what almost
// all of the schema is: a subtree that no second document touches is the mirror's own type, carried through
// unchanged. The first time a second document, a rename or an omission touches it, it is materialized into
// fields of its own.
type node struct {
	path string // the Ichor path, "" for the root

	ref      *srcStruct // while the node is only a reference
	refSinks []sink     // where the whole referenced struct goes

	fields []*field // once materialized

	doc string // Ichor's own description
}

// field is one setting of Ichor's schema.
type field struct {
	json     string
	t        *typ
	required bool
	sinks    []sink
	doc      string

	// docs is set on a collection of named documents: the kind of the document each entry is.
	docs string
	// list is set on a collection written as a list with a name in each entry rather than as a map.
	list bool

	// conflict records the sources that disagree on this field's type, for the report.
	spelledBy string
}

func (n *node) field(name string) *field {
	for _, f := range n.fields {
		if f.json == name {
			return f
		}
	}

	return nil
}

// refType is the type of a field that holds the mirror struct s whole, wrapped as the mirror wraps it, with
// the node that stands for it.
func (n *node) child(t *typ) *node {
	for ; t != nil; t = t.elem {
		if t.kind == tStruct {
			return t.child
		}
	}

	return nil
}

// cloneTyp copies a mirror type, giving each struct in it a reference node whose sink is base.
func cloneTyp(t *typ, path string, base sink) *typ {
	c := &typ{kind: t.kind, name: t.name}

	if t.elem != nil {
		c.elem = cloneTyp(t.elem, path, base)
	}

	if t.kind == tStruct {
		c.child = &node{path: path, refSinks: []sink{base}}
	}

	return c
}

func (g *gen) newRef(name string, sinks []sink, path string) *node {
	return &node{path: path, ref: g.src.structs[name], refSinks: sinks}
}

// materialize turns a reference into the fields it stands for.
func (g *gen) materialize(n *node) error {
	if n.ref == nil {
		return nil
	}

	ref, sinks := n.ref, n.refSinks
	n.ref, n.refSinks = nil, nil

	for _, f := range ref.fields {
		var fs []sink
		for _, s := range sinks {
			fs = append(fs, s.join(f.json))
		}

		if err := g.insert(n, f.json, f.t, f.required, fs, ""); err != nil {
			return err
		}
	}

	return nil
}

// insert adds a setting to n under name, or merges it with the setting of that name that is already there.
func (g *gen) insert(n *node, name string, t *typ, required bool, sinks []sink, doc string) error {
	if err := g.materialize(n); err != nil {
		return err
	}

	where := join(n.path, name)

	ex := n.field(name)
	if ex == nil {
		nf := &field{json: name, required: required, sinks: slices.Clone(sinks), doc: doc}
		nf.t = g.bind(t, where, sinks)
		n.fields = append(n.fields, nf)

		return nil
	}

	return g.merge(ex, t, required, sinks, where)
}

// bind copies t for a new field and hangs reference nodes on its structs.
func (g *gen) bind(t *typ, where string, sinks []sink) *typ {
	c := &typ{kind: t.kind, name: t.name}

	if t.elem != nil {
		c.elem = g.bind(t.elem, where, sinks)
	}

	if t.kind == tStruct {
		c.child = g.newRef(t.name, slices.Clone(sinks), where)
	}

	return c
}

// shape is what two types must share to be one setting: their JSON shape. Width of integers is not part of
// it, and neither is whether the Go type is a pointer.
func shape(t *typ) string {
	switch t.kind {
	case tBasic:
		switch {
		case strings.HasPrefix(t.name, "int"), strings.HasPrefix(t.name, "uint"):
			return "integer"
		case strings.HasPrefix(t.name, "float"):
			return "number"
		case t.name == "byte":
			return "byte"
		}

		return t.name
	case tStruct:
		return "object"
	case tOpaque:
		return t.name
	case tPtr:
		return shape(t.elem)
	case tSlice:
		if e := t.elem; e.kind == tBasic && (e.name == "uint8" || e.name == "byte") {
			return "bytes"
		}

		return "[]" + shape(t.elem)
	case tMap:
		return "map" + shape(t.elem)
	}

	return "?"
}

// merge adds a second spelling of an existing setting.
func (g *gen) merge(ex *field, t *typ, required bool, sinks []sink, where string) error {
	if shape(ex.t) != shape(t) {
		g.conflicts = append(g.conflicts, fmt.Sprintf("%s: %s against %s (%s)", where, shape(ex.t), shape(t), sinkList(sinks)))

		return nil
	}

	ex.required = ex.required && required
	ex.sinks = append(ex.sinks, sinks...)

	return g.mergeTypes(ex.t, t, where, sinks)
}

func sinkList(ss []sink) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.String())
	}

	return strings.Join(out, ", ")
}

// mergeTypes folds t into the type already placed: structs merge their fields, and a wider integer wins.
func (g *gen) mergeTypes(ex, t *typ, where string, sinks []sink) error {
	switch {
	case ex.kind == tBasic && t.kind == tBasic:
		if wider(t.name, ex.name) {
			ex.name = t.name
		}

		return nil
	case ex.kind == tStruct && t.kind == tStruct:
		if ex.child.ref != nil && ex.child.ref.name == t.name && sameSinks(ex.child.refSinks, sinks) {
			return nil
		}

		g.structMerges = append(g.structMerges, where)

		if err := g.materialize(ex.child); err != nil {
			return err
		}

		return g.addStruct(ex.child, g.src.structs[t.name], sinks, nil)
	case ex.elem != nil && t.elem != nil && ex.kind == t.kind:
		return g.mergeTypes(ex.elem, t.elem, where, sinks)
	case ex.kind == tPtr || t.kind == tPtr:
		a, b := ex, t
		if a.kind == tPtr {
			a = a.elem
		}

		if b.kind == tPtr {
			b = b.elem
		}

		return g.mergeTypes(a, b, where, sinks)
	}

	return nil
}

func sameSinks(a, b []sink) bool {
	return slices.Equal(a, b)
}

var intRank = map[string]int{"int8": 1, "uint8": 1, "int16": 2, "uint16": 2, "int32": 3, "uint32": 3, "int": 4, "uint": 4, "int64": 5, "uint64": 5}

// wider reports whether integer type a holds every value b does, or more.
func wider(a, b string) bool {
	return intRank[a] > intRank[b]
}

// addStruct adds every field of s to n, each going to sinks joined with its name. A name in skip is left out.
func (g *gen) addStruct(n *node, s *srcStruct, base []sink, skip map[string]bool) error {
	for _, f := range s.fields {
		if skip[f.json] {
			continue
		}

		var fs []sink
		for _, b := range base {
			fs = append(fs, b.join(f.json))
		}

		if err := g.insert(n, f.json, f.t, f.required, fs, ""); err != nil {
			return err
		}
	}

	return nil
}

func join(path, name string) string {
	if path == "" {
		return name
	}

	return path + "." + name
}
