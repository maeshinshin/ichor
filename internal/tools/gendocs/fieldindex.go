package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// patchCRD is the file the index is read from: the spec.config schema of TalosConfigPatch.
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

// generateFieldIndex writes the field index below file: one row for each field of each document kind that
// spec.config carries, in the path it has in the document, which is what the editor writes. The CRD gives the paths, types, required and format; the description and
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

	var config *apiextv1.JSONSchemaProps

	for _, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			if c, ok := v.Schema.OpenAPIV3Schema.Properties["spec"].Properties["config"]; ok {
				config = &c
			}
		}
	}

	if config == nil {
		return nil, fmt.Errorf("%s: no spec.config schema", patchCRD)
	}

	// The schema of each document kind, assembled from where spec.config puts the settings of the kind.
	docs := map[string]*setting{}
	collectDocuments(reflect.TypeFor[ichorconfig.Config](), *config, docs)

	var rows []fieldRow

	for _, kind := range slices.Sorted(maps.Keys(docs)) {
		key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)
		if !ok {
			return nil, fmt.Errorf("kind %q has no field in TalosConfigPatchDocument", kind)
		}

		schema := docs[kind].schema()
		mt := machineryType(key)
		rows = append(rows, fieldRow{Kind: kind, Description: kindDescription(mt, schema)})

		if slices.Contains(noFieldRows, kind) {
			continue
		}

		w := &indexWalker{kind: kind}
		w.below("", schema, mt)
		rows = append(rows, w.rows...)
	}

	return rows, nil
}

// collectDocuments walks Ichor's schema, the Go type and its CRD schema side by side, and puts each setting where
// the document that spells it has it. A setting several documents spell is a row of the one the converter writes:
// a document before the v1alpha1 one. A document kind's schema is the union of its settings, by their path in it.
func collectDocuments(t reflect.Type, s apiextv1.JSONSchemaProps, docs map[string]*setting) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")

		prop, ok := s.Properties[name]
		if !ok {
			continue
		}

		tag := f.Tag.Get("ichor")

		switch {
		case strings.HasPrefix(tag, "docs="):
			kind := strings.TrimPrefix(tag, "docs=")

			var entry *apiextv1.JSONSchemaProps

			if prop.AdditionalProperties != nil {
				entry = prop.AdditionalProperties.Schema
			} else if prop.Items != nil {
				entry = prop.Items.Schema
			}

			if entry != nil {
				// A document is named by its own field.
				put(docs, kind, "name", apiextv1.JSONSchemaProps{Type: "string"}, true)
				collectDocuments(structOf(f.Type), *entry, docs)
			}
		case structOf(f.Type).PkgPath() == t.PkgPath():
			// An object of Ichor's own schema: its tag only says where the whole of it goes in the v1alpha1 document.
			collectDocuments(structOf(f.Type), prop, docs)
		default:
			for _, sink := range documentSinks(tag) {
				put(docs, sink[0], sink[1], prop, slices.Contains(s.Required, name))
			}
		}
	}
}

// documentSinks is the "Kind:path" pairs of a setting's tag a row is written for: the documents' before the
// v1alpha1 document's, and none for a switch, which is the presence of a document and has no field of its own.
func documentSinks(tag string) [][2]string {
	var docs, config [][2]string

	for part := range strings.SplitSeq(tag, ";") {
		kind, path, _ := strings.Cut(part, ":")

		switch {
		case strings.HasPrefix(kind, "+"):
		case kind == "Config":
			config = append(config, [2]string{kind, path})
		default:
			docs = append(docs, [2]string{kind, path})
		}
	}

	if len(docs) > 0 {
		return docs
	}

	return config
}

// setting is a place in the schema of a document: a setting, or an object holding settings.
type setting struct {
	leaf     *apiextv1.JSONSchemaProps
	required bool
	kids     map[string]*setting
}

// put places a setting at path in the document of kind, creating the objects above it.
func put(docs map[string]*setting, kind, path string, leaf apiextv1.JSONSchemaProps, required bool) {
	at, ok := docs[kind]
	if !ok {
		at = &setting{}
		docs[kind] = at
	}

	for p := range strings.SplitSeq(path, ".") {
		if at.kids == nil {
			at.kids = map[string]*setting{}
		}

		next, ok := at.kids[p]
		if !ok {
			next = &setting{}
			at.kids[p] = next
		}

		at = next
	}

	at.leaf, at.required = &leaf, required
}

// schema is the CRD schema of the place: the setting's own, or an object of what is below it.
func (s *setting) schema() apiextv1.JSONSchemaProps {
	if s.leaf != nil {
		return *s.leaf
	}

	out := apiextv1.JSONSchemaProps{Type: "object", Properties: map[string]apiextv1.JSONSchemaProps{}}

	for name, k := range s.kids {
		out.Properties[name] = k.schema()

		if k.required {
			out.Required = append(out.Required, name)
		}
	}

	slices.Sort(out.Required)

	return out
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
