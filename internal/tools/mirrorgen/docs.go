package main

import (
	"go/ast"
	"go/types"
	"slices"
	"strings"
)

// docIndex is a (type name, field name) -> doc comment lookup built once per
// package by walking its syntax trees. Field name "" holds the type's own
// doc comment.
type docIndex map[string]map[string]string

// docIndexes is filled in lazily, one entry per package path that actually
// has syntax available (NeedSyntax was requested for pkgRuntime and
// pkgV1Alpha1 and their dependencies via NeedDeps, but we only ever need to
// look inside the two packages holding the allow-listed kinds' source).
var docIndexes = map[string]docIndex{}

func (g *generator) docIndexFor(pkgPath string) docIndex {
	if idx, ok := docIndexes[pkgPath]; ok {
		return idx
	}

	idx := docIndex{}

	p, ok := g.byPath[pkgPath]
	if ok {
		for _, file := range p.Syntax {
			indexFile(file, idx)
		}
	}

	docIndexes[pkgPath] = idx

	return idx
}

func indexFile(file *ast.File, idx docIndex) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			typeDoc := ts.Doc
			if typeDoc == nil {
				typeDoc = gd.Doc
			}

			fields := idx[ts.Name.Name]
			if fields == nil {
				fields = map[string]string{}
				idx[ts.Name.Name] = fields
			}

			if typeDoc != nil {
				fields[""] = typeDoc.Text()
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}

			for _, f := range st.Fields.List {
				if f.Doc == nil || len(f.Names) == 0 {
					continue
				}

				for _, n := range f.Names {
					fields[n.Name] = f.Doc.Text()
				}
			}
		}
	}
}

func (g *generator) typeDoc(n *types.Named) string {
	pkg := n.Obj().Pkg()
	if pkg == nil {
		return ""
	}

	idx := g.docIndexFor(pkg.Path())

	return extractDoc(idx[n.Obj().Name()][""])
}

func (g *generator) fieldDoc(structName, fieldName string) string {
	n, ok := g.nameOwner[structName]
	if !ok {
		return ""
	}

	pkg := n.Obj().Pkg()
	if pkg == nil {
		return ""
	}

	idx := g.docIndexFor(pkg.Path())

	return extractDoc(idx[structName][fieldName])
}

// docgenKeys are the keys Talos's own doc-generation tool recognizes in its
// mini-YAML-in-a-comment format (see e.g. config/types/v1alpha1/v1alpha1_types.go).
// extractDoc pulls the human-readable text out of that format without
// implementing the rest of it: either the value of "description:", or, if
// there is none, the prose that precedes the first such key.
var docgenKeys = []string{
	"description:", "examples:", "alias:", "schemaRoot:",
	"schemaMeta:", "schema:", "schemaRequired:", "values:",
}

func extractDoc(raw string) string {
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "description:") {
			continue
		}

		indent := leadingSpaces(line)
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "description:"))

		if rest != "" && rest != "|" && rest != "|-" && rest != ">" {
			return rest
		}

		var buf []string

		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "" {
				buf = append(buf, "")

				continue
			}

			if leadingSpaces(lines[j]) <= indent {
				break
			}

			buf = append(buf, strings.TrimSpace(lines[j]))
		}

		return strings.TrimSpace(strings.Join(buf, "\n"))
	}

	var prose []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if slices.ContainsFunc(docgenKeys, func(k string) bool { return strings.HasPrefix(trimmed, k) }) {
			break
		}

		if trimmed != "" {
			prose = append(prose, trimmed)
		}
	}

	return strings.TrimSpace(strings.Join(prose, " "))
}

func leadingSpaces(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
