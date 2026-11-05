package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// generateCRDs replaces dir with one Markdown page per CRD found in crdsDir.
func generateCRDs(crdsDir, dir string) error {
	files, err := filepath.Glob(filepath.Join(crdsDir, "*.yaml"))
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return fmt.Errorf("no CRDs in %s", crdsDir)
	}

	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}

		var crd apiextv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}

		name := strings.ToLower(crd.Spec.Names.Kind) + ".md"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(renderCRD(&crd)), 0o644); err != nil {
			return err
		}
	}

	return nil
}

// renderCRD writes the page of one CRD: its names, then for each served version the field tree of
// its schema as a table whose first column is the full path of the field.
func renderCRD(crd *apiextv1.CustomResourceDefinition) string {
	kind := crd.Spec.Names.Kind

	var b strings.Builder

	b.WriteString(frontmatter(kind, firstSentence(topDescription(crd))))
	b.WriteString("# " + kind + "\n\n")
	b.WriteString("This page is generated from the CRD schema in `config/crd/bases` (`make docs-reference`). Do not edit it.\n\n")

	if d := topDescription(crd); d != "" {
		b.WriteString(paragraphs(d) + "\n\n")
	}

	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Group | `%s` |\n", crd.Spec.Group)
	fmt.Fprintf(&b, "| Kind | `%s` (list kind `%s`) |\n", kind, crd.Spec.Names.ListKind)
	fmt.Fprintf(&b, "| Resource | `%s` |\n", crd.Spec.Names.Plural)

	if len(crd.Spec.Names.ShortNames) > 0 {
		fmt.Fprintf(&b, "| Short names | `%s` |\n", strings.Join(crd.Spec.Names.ShortNames, "`, `"))
	}

	fmt.Fprintf(&b, "| Scope | %s |\n\n", crd.Spec.Scope)

	for _, v := range crd.Spec.Versions {
		if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}

		fmt.Fprintf(&b, "## %s\n\n", v.Name)

		notes := []string{}
		if !v.Served {
			notes = append(notes, "not served")
		}

		if v.Subresources != nil && v.Subresources.Status != nil {
			notes = append(notes, "has a `status` subresource")
		}

		if len(notes) > 0 {
			b.WriteString("Version " + v.Name + ": " + strings.Join(notes, ", ") + ".\n\n")
		}

		root := v.Schema.OpenAPIV3Schema
		for _, top := range sortedKeys(root.Properties) {
			if top == "apiVersion" || top == "kind" || top == "metadata" {
				continue
			}

			prop := root.Properties[top]
			fmt.Fprintf(&b, "### %s\n\n", top)
			b.WriteString("| Field | Type | Required | Description |\n|---|---|---|---|\n")

			w := &walker{b: &b}
			w.walk(top, prop, slices.Contains(root.Required, top), false)
			b.WriteString("\n")
		}
	}

	return b.String()
}

// walker writes one table row per field below a schema node.
type walker struct{ b *strings.Builder }

func (w *walker) walk(path string, s apiextv1.JSONSchemaProps, required, parentDeprecated bool) {
	deprecated := parentDeprecated || strings.Contains(strings.ToLower(s.Description), "deprecated")

	label := "`" + path + "`"
	if deprecated {
		label += " **(deprecated)**"
	}

	req := "no"
	if required {
		req = "yes"
	}

	fmt.Fprintf(w.b, "| %s | %s | %s | %s |\n", label, typeName(s), req, cell(describe(s)))

	w.below(path, s, deprecated)
}

// below writes the rows under a node: its properties, and those of its array elements and map
// values. An element or value has no row of its own; its type is part of the parent's.
func (w *walker) below(path string, s apiextv1.JSONSchemaProps, deprecated bool) {
	for _, name := range sortedKeys(s.Properties) {
		w.walk(path+"."+name, s.Properties[name], slices.Contains(s.Required, name), deprecated)
	}

	if s.Items != nil && s.Items.Schema != nil && hasChildren(*s.Items.Schema) {
		w.below(path+"[]", *s.Items.Schema, deprecated)
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && hasChildren(*s.AdditionalProperties.Schema) {
		w.below(path+".<key>", *s.AdditionalProperties.Schema, deprecated)
	}
}

func hasChildren(s apiextv1.JSONSchemaProps) bool {
	return len(s.Properties) > 0 || (s.Items != nil && s.Items.Schema != nil && hasChildren(*s.Items.Schema)) ||
		(s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && hasChildren(*s.AdditionalProperties.Schema))
}

// typeName is the schema type as a reader writes it: string, []string, map[string]integer, object.
func typeName(s apiextv1.JSONSchemaProps) string {
	return "`" + rawType(s) + "`"
}

func rawType(s apiextv1.JSONSchemaProps) string {
	t := s.Type

	switch {
	case s.XIntOrString:
		t = "integer or string"
	case t == "array" && s.Items != nil && s.Items.Schema != nil:
		t = "[]" + rawType(*s.Items.Schema)
	case t == "object" && s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil:
		t = "map[string]" + rawType(*s.AdditionalProperties.Schema)
	case t == "object" && len(s.Properties) == 0 && s.XPreserveUnknownFields != nil && *s.XPreserveUnknownFields:
		t = "object (free-form)"
	case t == "":
		t = "any"
	}

	if s.Format != "" && s.Format != "int32" && s.Format != "int64" {
		t += " (" + s.Format + ")"
	}

	return t
}

// describe is the schema description followed by the constraints a reader needs to fill the field.
func describe(s apiextv1.JSONSchemaProps) string {
	parts := []string{strings.TrimSpace(s.Description)}

	elem := s
	if s.Items != nil && s.Items.Schema != nil {
		elem = *s.Items.Schema
	}

	if len(elem.Enum) > 0 {
		vals := make([]string, 0, len(elem.Enum))
		for _, e := range elem.Enum {
			vals = append(vals, "`"+strings.Trim(string(e.Raw), `"`)+"`")
		}

		parts = append(parts, "Allowed values: "+strings.Join(vals, ", ")+".")
	}

	if s.Default != nil {
		parts = append(parts, "Default: `"+string(s.Default.Raw)+"`.")
	}

	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " ")
}

// cell makes text safe for one line of a table: one line, pipes escaped, angle brackets not read
// as HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "<", `\<`)

	return s
}

// paragraphs keeps the paragraph breaks of a description and drops the hard wraps inside one.
func paragraphs(s string) string {
	paras := strings.Split(strings.TrimSpace(s), "\n\n")
	out := make([]string, 0, len(paras))

	for _, p := range paras {
		out = append(out, strings.Join(strings.Fields(p), " "))
	}

	return strings.Join(out, "\n\n")
}

func topDescription(crd *apiextv1.CustomResourceDefinition) string {
	for _, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil && v.Schema.OpenAPIV3Schema.Description != "" {
			return v.Schema.OpenAPIV3Schema.Description
		}
	}

	return ""
}

// firstSentence is the description's first sentence, for the page's summary line.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}

	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
