package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// patchCRD is the file the index is read from: the documents[] union of TalosConfigPatch.
const patchCRD = "ichor.maesh.dev_talosconfigpatches.yaml"

// noFieldRows lists the kinds the index holds a single row for and no field rows: the static pod is the
// whole core v1 Pod, 1,075 fields of Kubernetes' own, 47% of the rows and half of the index's gzip, and
// every one of them would match a search for name, image or ports. The kind stays reachable by its row.
// shortcut: a fixed list; drop it if a second kind turns out to be that large.
var noFieldRows = []string{"KubeStaticPodConfig"}

// fieldRow is one entry of the UI's field search. Path is relative to the document (the object under
// the kind's key), in the notation of the CRD reference: [] for an array element, <key> for a map value.
// A kind's own row has an empty path.
type fieldRow struct {
	Kind        string   `json:"kind"`
	Path        string   `json:"path,omitempty"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Format      string   `json:"format,omitempty"`
	Values      []string `json:"values,omitempty"`
	Description string   `json:"description,omitempty"`
}

// generateFieldIndex writes the field index below file: one row for each field of each document kind in
// the TalosConfigPatch schema. The CRD gives the paths, types, required and format; the description and
// the allowed values come from the Talos machinery documentation tables (the Doc method of each document
// type), which the CRD schema does not carry.
func generateFieldIndex(crdsDir, file string) error {
	rows, err := fieldRows(crdsDir)
	if err != nil {
		return err
	}

	// One row to a line keeps a diff of the index readable.
	var b bytes.Buffer

	b.WriteString("[\n")

	for i, r := range rows {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}

		b.Write(raw)

		if i < len(rows)-1 {
			b.WriteString(",")
		}

		b.WriteString("\n")
	}

	b.WriteString("]\n")

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}

	return os.WriteFile(file, b.Bytes(), 0o644)
}

func fieldRows(crdsDir string) ([]fieldRow, error) {
	raw, err := os.ReadFile(filepath.Join(crdsDir, patchCRD))
	if err != nil {
		return nil, err
	}

	var crd apiextv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		return nil, fmt.Errorf("%s: %w", patchCRD, err)
	}

	var docs *apiextv1.JSONSchemaProps

	for _, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			spec := v.Schema.OpenAPIV3Schema.Properties["spec"]
			if d, ok := spec.Properties["documents"]; ok && d.Items != nil {
				docs = d.Items.Schema
			}
		}
	}

	if docs == nil {
		return nil, fmt.Errorf("%s: no spec.documents[] schema", patchCRD)
	}

	var rows []fieldRow

	for _, kindEnum := range docs.Properties["kind"].Enum {
		kind := strings.Trim(string(kindEnum.Raw), `"`)

		key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)
		if !ok {
			return nil, fmt.Errorf("kind %q has no field in TalosConfigPatchDocument", kind)
		}

		schema, ok := docs.Properties[key]
		if !ok {
			return nil, fmt.Errorf("kind %q: no %q in the schema", kind, key)
		}

		mt := machineryType(key)
		root := fieldRow{Kind: kind, Description: kindDescription(mt, schema)}
		rows = append(rows, root)

		if slices.Contains(noFieldRows, kind) {
			continue
		}

		w := &indexWalker{kind: kind}
		w.below("", schema, mt)
		rows = append(rows, w.rows...)
	}

	return rows, nil
}

// machineryType is the Talos machinery document type behind the field key of TalosConfigPatchDocument: the
// type ToMachinery of the mirror returns, found without calling it. nil when there is none.
func machineryType(key string) reflect.Type {
	f, ok := fieldByJSONName(reflect.TypeFor[ichorv1alpha1.TalosConfigPatchDocument](), key)
	if !ok {
		return nil
	}

	m, ok := f.Type.MethodByName("ToMachinery")
	if !ok || m.Type.NumOut() == 0 {
		return nil
	}

	return deref(m.Type.Out(0))
}

func fieldByJSONName(t reflect.Type, name string) (reflect.StructField, bool) {
	for sf := range t.Fields() {
		if n, _, _ := strings.Cut(sf.Tag.Get("json"), ","); n == name {
			return sf, true
		}
	}

	return reflect.StructField{}, false
}

// kindDescription is the type-level text of a document kind: Talos's, else the CRD's.
func kindDescription(mt reflect.Type, s apiextv1.JSONSchemaProps) string {
	if d := docOf(mt); d != nil && d.Description != "" {
		return tidy(d.Description)
	}

	return tidy(s.Description)
}

// indexWalker collects the rows of one kind. It walks the CRD schema and the machinery type side by side.
type indexWalker struct {
	kind string
	rows []fieldRow
}

// below adds a row for each property of s, and of its array elements and map values, which have none of
// their own. mt is the machinery struct that s mirrors, nil when there is none (a type machinery does not
// document, or a field the mirror renames).
func (w *indexWalker) below(path string, s apiextv1.JSONSchemaProps, mt reflect.Type) {
	for _, name := range sortedKeys(s.Properties) {
		prop := s.Properties[name]
		p := name
		if path != "" {
			p = path + "." + name
		}

		ft, doc := lookupField(mt, name)
		r := fieldRow{
			Kind:        w.kind,
			Path:        p,
			Type:        strings.TrimSuffix(rawType(prop), " ("+prop.Format+")"),
			Required:    slices.Contains(s.Required, name),
			Format:      prop.Format,
			Description: tidy(prop.Description),
		}

		if doc != nil {
			if doc.Description != "" {
				r.Description = tidy(doc.Description)
			}

			// Only a string field takes its Values as written: a boolean's are its spellings, a map's or an
			// array's are examples, and an integer enumeration is an ordinal in the CRD, not these names.
			if prop.Type == "string" {
				r.Values = doc.Values
			}
		}

		w.rows = append(w.rows, r)
		w.element(p, prop, structOf(ft))
	}
}

// element continues below a property into what its array elements or map values contain.
func (w *indexWalker) element(path string, s apiextv1.JSONSchemaProps, mt reflect.Type) {
	if s.Items != nil && s.Items.Schema != nil && hasChildren(*s.Items.Schema) {
		w.element(path+"[]", *s.Items.Schema, mt)
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && hasChildren(*s.AdditionalProperties.Schema) {
		w.element(path+".<key>", *s.AdditionalProperties.Schema, mt)
	}

	w.below(path, s, mt)
}

// structOf is the struct type behind a field type: through pointers, slices and maps.
func structOf(t reflect.Type) reflect.Type {
	for t != nil {
		t = deref(t)

		if t.Kind() != reflect.Slice && t.Kind() != reflect.Map {
			break
		}

		t = t.Elem()
	}

	return t
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// docOf is the documentation table of a machinery type: its Doc method, which Talos generates.
func docOf(t reflect.Type) *encoder.Doc {
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}

	d, ok := reflect.TypeAssert[interface{ Doc() *encoder.Doc }](reflect.New(t))
	if !ok {
		return nil
	}

	return d.Doc()
}

// lookupField finds the field of the machinery struct t that is written name in YAML, through embedded
// structs, with its documentation entry from the table of the struct that declares it.
func lookupField(t reflect.Type, name string) (reflect.Type, *encoder.Doc) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, nil
	}

	for sf := range t.Fields() {
		tag, opts, _ := strings.Cut(sf.Tag.Get("yaml"), ",")
		if tag == "-" {
			continue
		}

		if tag == "" && (sf.Anonymous || strings.Contains(opts, "inline")) {
			if ft, d := lookupField(deref(sf.Type), name); ft != nil {
				return ft, d
			}

			continue
		}

		if tag != name {
			continue
		}

		var doc *encoder.Doc

		if table := docOf(t); table != nil {
			for i := range table.Fields {
				if table.Fields[i].Name == name {
					doc = &table.Fields[i]
				}
			}
		}

		return sf.Type, doc
	}

	return nil, nil
}

// tidy keeps a description's paragraphs and drops the hard line wraps inside them.
func tidy(s string) string {
	return paragraphs(s)
}
