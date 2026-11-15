package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// patchCRD is the file the index is read from: the spec.config schema of TalosConfigPatch.
const patchCRD = "ichor.maesh.dev_talosconfigpatches.yaml"

// noFieldRows lists the kinds the index holds a single row for and no field rows: the static pod is the
// whole core v1 Pod, 1,075 fields of Kubernetes' own, 47% of the rows and half of the index's gzip, and
// every one of them would match a search for name, image or ports. The collection stays reachable by its row.
// shortcut: a fixed list; drop it if a second kind turns out to be that large.
var noFieldRows = []string{"KubeStaticPodConfig"}

// fieldRow is one entry of the UI's field search: one setting of spec.config, by the path it has there, with
// [] for an array element and <key> for a map value. A setting is a row once, whatever number of Talos
// documents spell it.
type fieldRow struct {
	Path        string   `json:"path"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Format      string   `json:"format,omitempty"`
	Values      []string `json:"values,omitempty"`
	Description string   `json:"description,omitempty"`
	// Doc is where the Talos document that spells the setting has it, as "Kind:path" (the path is empty on a
	// whole document, and name on the key of an entry). The index does not carry it: the wire is spec.config and
	// nothing in the UI needs a document. The generator's tests check each setting is the row of one document
	// spelling, which is why it is kept in memory.
	Doc string `json:"-"`
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

	w := &indexer{}
	w.object(reflect.TypeFor[ichorconfig.Config](), *config, "")

	slices.SortStableFunc(w.rows, func(a, b fieldRow) int { return strings.Compare(a.Path, b.Path) })

	return w.rows, w.err
}

// indexer walks spec.config's Go type and its CRD schema side by side, and writes a row for each setting.
type indexer struct {
	rows []fieldRow
	err  error
}

func join(path, name string) string {
	if path == "" {
		return name
	}

	return path + "." + name
}

// object walks the fields of one object of the schema. A field's ichor tag says where the document that
// spells it has it: an object of Ichor's own schema is only the way to its fields, a collection ("docs=")
// is a document of its own for each entry, and any other field is a setting, with everything below it.
func (w *indexer) object(t reflect.Type, s apiextv1.JSONSchemaProps, path string) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")

		prop, ok := s.Properties[name]
		if !ok {
			continue
		}

		p, tag, required := join(path, name), f.Tag.Get("ichor"), slices.Contains(s.Required, name)

		switch {
		case strings.HasPrefix(tag, "docs="):
			w.collection(strings.TrimPrefix(tag, "docs="), f.Type, prop, p, required)
		case structOf(f.Type).PkgPath() == t.PkgPath():
			w.container(f.Type, prop, p, tag, required)
		default:
			w.setting(prop, p, tag, required)
		}
	}
}

// firstSink is the "Kind:path" a setting's tag is read from: the document's before the v1alpha1 document's, and
// none for a switch, which is the presence of a document and has no field of its own.
func firstSink(tag string) (kind, path string) {
	var config [2]string

	for part := range strings.SplitSeq(tag, ";") {
		k, p, _ := strings.Cut(part, ":")

		switch {
		case strings.HasPrefix(k, "+"):
		case k == "Config":
			config = [2]string{k, p}
		default:
			return k, p
		}
	}

	return config[0], config[1]
}

// talos is the machinery type at a path of a document kind, with the documentation entry of its field.
func talos(kind, path string) (reflect.Type, *encoder.Doc) {
	key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)
	if !ok {
		return nil, nil
	}

	mt := machineryType(key)
	if path == "" {
		return mt, nil
	}

	var (
		ft  reflect.Type
		doc *encoder.Doc
	)

	for seg := range strings.SplitSeq(path, ".") {
		if ft, doc = lookupField(mt, seg); ft == nil {
			return nil, nil
		}

		mt = structOf(ft)
	}

	return ft, doc
}

func (w *indexer) row(p string, prop apiextv1.JSONSchemaProps, required bool, doc string, d *encoder.Doc) {
	r := fieldRow{
		Path:        p,
		Type:        strings.TrimSuffix(rawType(prop), " ("+prop.Format+")"),
		Required:    required,
		Format:      prop.Format,
		Description: tidy(prop.Description),
		Doc:         doc,
	}

	if d != nil {
		if d.Description != "" {
			r.Description = tidy(d.Description)
		}

		// Only a string field takes its Values as written: a boolean's are its spellings, a map's or an
		// array's are examples, and an integer enumeration is an ordinal in the CRD, not these names.
		if prop.Type == "string" {
			r.Values = d.Values
		}
	}

	w.rows = append(w.rows, r)
}

// setting is a field the document spells at a path of its own, and the fields below it.
func (w *indexer) setting(prop apiextv1.JSONSchemaProps, p, tag string, required bool) {
	kind, docPath := firstSink(tag)
	if kind == "" {
		w.err = errors.Join(w.err, fmt.Errorf("%s has no document to write it in", p))

		return
	}

	mt, d := talos(kind, docPath)
	w.row(p, prop, required, kind+":"+docPath, d)

	bw := &subtree{indexer: w, kind: kind, docBase: docPath, specBase: p}
	bw.element(docPath, prop, structOf(mt))
}

// container is an object of Ichor's own schema. Its row is the object as a whole, written at the path its tag
// gives; an object with no tag only groups settings, and has no row.
func (w *indexer) container(ft reflect.Type, prop apiextv1.JSONSchemaProps, p, tag string, required bool) {
	if kind, docPath := firstSink(tag); kind != "" {
		_, d := talos(kind, docPath)
		w.row(p, prop, required, kind+":"+docPath, d)
	}

	w.object(structOf(ft), entry(prop), p+suffix(prop))
}

// collection is a map or an array of documents of one kind, an entry each. Its row is the kind, and the entry's
// key is the document's name.
func (w *indexer) collection(kind string, ft reflect.Type, prop apiextv1.JSONSchemaProps, p string, required bool) {
	key, _ := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)

	mt := machineryType(key)
	if mt == nil {
		w.err = errors.Join(w.err, fmt.Errorf("%s: kind %q has no machinery type", p, kind))
	}

	desc := tidy(prop.Description)
	if d := docOf(mt); d != nil && d.Description != "" {
		desc = tidy(d.Description)
	}

	w.rows = append(w.rows, fieldRow{Path: p, Type: rawType(prop), Required: required, Description: desc, Doc: kind + ":"})

	e := p + suffix(prop)
	if prop.AdditionalProperties != nil {
		// A map's key is the name of the document.
		_, d := talos(kind, "name")
		w.row(e, apiextv1.JSONSchemaProps{Type: "string"}, true, kind+":name", d)
	}

	if !slices.Contains(noFieldRows, kind) {
		w.object(structOf(ft), entry(prop), e)
	}
}

// entry is the schema of an array's elements or a map's values, or the object itself.
func entry(s apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	switch {
	case s.Items != nil && s.Items.Schema != nil:
		return *s.Items.Schema
	case s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil:
		return *s.AdditionalProperties.Schema
	}

	return s
}

func suffix(s apiextv1.JSONSchemaProps) string {
	switch {
	case s.Items != nil && s.Items.Schema != nil:
		return "[]"
	case s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil:
		return ".<key>"
	}

	return ""
}

// objectType is the type of a schema that holds properties.
const objectType = "object"

// generateFieldIndex writes the field index below file: one row for each setting of spec.config, by its path
// there. The CRD gives the paths, types, required and format; the description and the allowed values come from
// the Talos machinery documentation tables (the Doc method of each document type), which the CRD schema does
// not carry.
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

// subtree walks the CRD schema of a setting and the machinery type that mirrors it side by side. docBase is the
// setting's path in its document and specBase its path in spec.config: a row below it has the same rest in both.
type subtree struct {
	*indexer
	kind     string
	docBase  string
	specBase string
}

// rows adds a row for each property of s, and of its array elements and map values, which have none of
// their own. mt is the machinery struct that s mirrors, nil when there is none (a type machinery does not
// document, or a field the mirror renames).
func (w *subtree) below(path string, s apiextv1.JSONSchemaProps, mt reflect.Type) {
	for _, name := range sortedKeys(s.Properties) {
		prop := s.Properties[name]
		p := join(path, name)

		ft, doc := lookupField(mt, name)
		w.row(w.specBase+strings.TrimPrefix(p, w.docBase), prop, slices.Contains(s.Required, name), w.kind+":"+p, doc)
		w.element(p, prop, structOf(ft))
	}
}

// element continues below a property into what its array elements or map values contain.
func (w *subtree) element(path string, s apiextv1.JSONSchemaProps, mt reflect.Type) {
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
