package main

import (
	"fmt"
	"strings"
)

// gen is one run of the generator: the mirror it reads, the tree it builds, and what it noticed on the way.
type gen struct {
	src  *source
	root *node

	conflicts    []string // settings two documents spell with different shapes and no row resolves
	structMerges []string // places where two documents' structs were folded into one
	listMerges   []string // merges that happened under a list or a map, which a row has to handle
	relaxed      []string // required fields made optional where documents meet
	carried      map[string]bool
}

// run builds Ichor's tree from the mirror and the rows in place.go.
func run(src *source) (*gen, error) {
	g := &gen{src: src, carried: map[string]bool{}}

	cfg := src.roots["Config"]
	if cfg == "" {
		return nil, fmt.Errorf("no Config document in the mirror")
	}

	g.root = g.newRef(cfg, []sink{{"Config", ""}}, "")

	if err := g.materialize(g.root); err != nil {
		return nil, err
	}

	for _, p := range droppedFromConfig {
		if err := g.drop(p.path); err != nil {
			return nil, fmt.Errorf("dropping %s: %w", p.path, err)
		}
	}

	for _, r := range rows {
		if err := g.place(r); err != nil {
			return nil, fmt.Errorf("%s: %w", r.kind, err)
		}
	}

	for path, kind := range presence {
		i := strings.LastIndexByte(path, '.')

		n, err := g.at(path[:i])
		if err != nil {
			return nil, fmt.Errorf("presence %s: %w", path, err)
		}

		f := n.field(path[i+1:])
		if f == nil {
			return nil, fmt.Errorf("presence %s: no such setting", path)
		}

		f.sinks = append(f.sinks, sink{"+" + kind, ""})
	}

	for path, doc := range sections {
		if doc == "" {
			continue
		}

		n, err := g.at(path)
		if err != nil {
			return nil, fmt.Errorf("section %s: %w", path, err)
		}

		n.doc = doc
	}

	g.settle(g.root)

	if err := g.check(g.root); err != nil {
		return nil, err
	}

	return g, nil
}

// settle makes a field optional wherever its object is the meeting place of more than one document: a patch
// is a sparse overlay, and a field one document requires cannot be required of a patch that only wrote what
// another document spells. Talos validates the finished configuration.
func (g *gen) settle(n *node) {
	kinds := map[string]bool{}
	for _, f := range n.fields {
		for _, s := range f.sinks {
			kinds[s.kind] = true
		}
	}

	for _, f := range n.fields {
		if len(kinds) > 1 && f.required {
			f.required = false
			g.relaxed = append(g.relaxed, join(n.path, f.json))
		}

		if c := n.child(f.t); c != nil && c.ref == nil {
			g.settle(c)
		}
	}
}

// at walks to the node at path, making the sections the path needs.
func (g *gen) at(path string) (*node, error) {
	n := g.root

	if path == "" {
		return n, nil
	}

	for i, seg := range strings.Split(path, ".") {
		if err := g.materialize(n); err != nil {
			return nil, err
		}

		f := n.field(seg)
		if f == nil {
			child := &node{path: join(n.path, seg)}
			f = &field{json: seg, t: &typ{kind: tPtr, elem: &typ{kind: tStruct, child: child}}}
			n.fields = append(n.fields, f)
		}

		c := n.child(f.t)
		if c == nil {
			return nil, fmt.Errorf("%s is not an object (at %s)", join(n.path, seg), strings.Join(strings.Split(path, ".")[:i+1], "."))
		}

		n = c
	}

	return n, g.materialize(n)
}

// drop removes the Config setting at path: a document spells it, and a setting is written once.
func (g *gen) drop(path string) error {
	i := strings.LastIndexByte(path, '.')

	parent, name := "", path
	if i >= 0 {
		parent, name = path[:i], path[i+1:]
	}

	n, err := g.at(parent)
	if err != nil {
		return err
	}

	for k, f := range n.fields {
		if f.json == name {
			n.fields = append(n.fields[:k], n.fields[k+1:]...)

			return nil
		}
	}

	return fmt.Errorf("there is no such setting in Config")
}

func (g *gen) place(r row) error {
	st := g.src.structs[g.src.roots[r.kind]]
	if st == nil {
		return fmt.Errorf("no such kind in the mirror")
	}

	g.carried[r.kind] = true

	skip := map[string]bool{"name": r.keyed != "" || r.keyedList != ""}
	if !skip["name"] {
		delete(skip, "name")
	}

	for f := range r.skip {
		skip[f] = true
	}

	for f := range r.move {
		skip[f] = true
	}

	switch {
	case r.keyed != "" || r.keyedList != "":
		return g.placeKeyed(r, st, skip)
	default:
		n, err := g.at(r.at)
		if err != nil {
			return err
		}

		if n.doc == "" {
			n.doc = r.doc
		}

		if err := g.addStruct(n, st, []sink{{r.kind, ""}}, skip); err != nil {
			return err
		}
	}

	for from, to := range r.move {
		var f *srcField

		for _, sf := range st.fields {
			if sf.json == from {
				f = sf
			}
		}

		if f == nil {
			return fmt.Errorf("move: %s has no field %s", r.kind, from)
		}

		i := strings.LastIndexByte(to, '.')

		parent, name := "", to
		if i >= 0 {
			parent, name = to[:i], to[i+1:]
		}

		n, err := g.at(parent)
		if err != nil {
			return err
		}

		if err := g.insert(n, name, f.t, f.required, []sink{{r.kind, from}}, ""); err != nil {
			return err
		}
	}

	return nil
}

// placeKeyed adds a collection of documents of one kind, each entry named by its key.
func (g *gen) placeKeyed(r row, st *srcStruct, skip map[string]bool) error {
	path, list := r.keyed, false
	if r.keyedList != "" {
		path, list = r.keyedList, true
	}

	i := strings.LastIndexByte(path, '.')

	parent, name := "", path
	if i >= 0 {
		parent, name = path[:i], path[i+1:]
	}

	n, err := g.at(parent)
	if err != nil {
		return err
	}

	if n.field(name) != nil {
		return fmt.Errorf("%s already holds a setting called %s: drop it from Config first or give the collection another name", n.path, name)
	}

	item := &node{path: join(path, "*"), doc: r.doc}
	if list {
		// A list keeps the document's own name field: the name is the entry's key.
		skip = map[string]bool{}
		for f := range r.skip {
			skip[f] = true
		}
	}

	if err := g.addStruct(item, st, []sink{{r.kind, ""}}, skip); err != nil {
		return err
	}

	t := &typ{kind: tStruct, name: "", child: item}
	if list {
		t = &typ{kind: tSlice, elem: &typ{kind: tPtr, elem: t}}
	} else {
		t = &typ{kind: tMap, elem: &typ{kind: tPtr, elem: t}}
	}

	n.fields = append(n.fields, &field{json: name, t: t, docs: r.kind, list: list, doc: r.doc, sinks: []sink{{r.kind, ""}}})

	return nil
}

// check refuses a shape the conversion cannot carry: documents merged into the entries of a list or a map that
// is not a collection of documents, where a setting could not be told which entry it belongs to.
func (g *gen) check(n *node) error {
	for _, f := range n.fields {
		c := n.child(f.t)
		if c == nil || c.ref != nil {
			continue
		}

		if f.docs == "" && f.t.kind != tPtr {
			return fmt.Errorf("%s: documents were merged into the entries of a list or map; write a row that gives the entries a name", join(n.path, f.json))
		}

		if err := g.check(c); err != nil {
			return err
		}
	}

	return nil
}
