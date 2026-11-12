/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package typedconfig_test

import (
	"bytes"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	yaml "go.yaml.in/yaml/v4"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// TestShippedKindsRoundTrip runs every kind in TalosConfigPatchDocument (so every kind mirrorgen ships, today
// and later) through the mirror and compares the result with what Talos itself makes of the same document.
//
// For each kind a machinery document is filled with a distinct value in every field the mirror carries, written
// out by Talos's own marshaller and read back by Talos's own loader: that is the document Talos produces. It
// then goes to the mirror, through JSON (the form a TalosConfigPatch stores, with unknown fields refused) and
// back to machinery, and the two must marshal to the same YAML. The mirror's JSON must also have the key
// layout of the YAML, which is what flattening an inline embedded struct has to keep: a patch author writes
// the document the way Talos documents it.
//
// A field the mirror deliberately does not carry (meta.Unstructured, the secret table) is left empty, and so is
// a field mirrored as an upstream type: the legacy Config document's own test builds those by hand.
func TestShippedKindsRoundTrip(t *testing.T) {
	doc := reflect.TypeFor[ichorv1alpha1.TalosConfigPatchDocument]()

	kinds := 0

	for i := range doc.NumField() {
		f := doc.Field(i)
		if f.Name == "Kind" {
			continue
		}

		kinds++

		t.Run(f.Name, func(t *testing.T) { roundTripKind(t, f.Name, f.Type.Elem()) })
	}

	if kinds < 7 {
		t.Fatalf("found %d kinds in TalosConfigPatchDocument, fewer than were ever shipped", kinds)
	}
}

func roundTripKind(t *testing.T, kind string, mirror reflect.Type) {
	t.Helper()

	var d ichorv1alpha1.TalosConfigPatchDocument

	d.Kind = kind
	reflect.ValueOf(&d).Elem().FieldByName(kind).Set(reflect.New(mirror))

	base, err := d.Document() // an empty document, with its kind and API version set
	if err != nil {
		t.Fatalf("building the empty %s document: %v", kind, err)
	}

	f := &filler{t: t}
	f.fill(reflect.ValueOf(base).Elem(), mirror, 0)

	written, err := yaml.Marshal(base)
	if err != nil {
		t.Fatalf("Talos marshalling %s: %v", kind, err)
	}

	provider, err := configloader.NewFromBytes(written, configloader.WithNoValidation())
	if err != nil {
		t.Fatalf("Talos reading back its own %s document: %v\n%s", kind, err, written)
	}

	var talos config.Document

	for _, c := range provider.(interface{ Documents() []config.Document }).Documents() {
		if c.Kind() == base.Kind() {
			talos = c
		}
	}

	if talos == nil {
		t.Fatalf("Talos's loader returned no %s document from:\n%s", base.Kind(), written)
	}

	want, err := yaml.Marshal(talos)
	if err != nil {
		t.Fatalf("marshalling Talos's %s document: %v", kind, err)
	}

	typed, err := ichorv1alpha1.TalosConfigPatchDocumentFromMachinery(talos)
	if err != nil {
		t.Fatalf("FromMachinery %s: %v", kind, err)
	}

	stored, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("encoding the typed %s document: %v", kind, err)
	}

	var back ichorv1alpha1.TalosConfigPatchDocument

	dec := json.NewDecoder(bytes.NewReader(stored))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&back); err != nil {
		t.Fatalf("decoding the stored %s document: %v\n%s", kind, err, stored)
	}

	doc, err := back.Document()
	if err != nil {
		t.Fatalf("ToMachinery %s: %v", kind, err)
	}

	got, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling the round-tripped %s: %v", kind, err)
	}

	if string(got) != string(want) {
		t.Errorf("%s does not round trip.\n--- Talos ---\n%s\n--- through the mirror ---\n%s", kind, want, got)
	}

	// shortcut: the legacy Config document is not layout-checked, because its required fields that the mirror
	// drops are written as empty keys, and the mirror writes an unset duration as "0s"; the hand-built fixture
	// of TestRoundTripConfig covers it. Upgrade by comparing only keys with a value.
	if kind != "Config" {
		checkKeyLayout(t, kind, want, stored)
	}
}

// checkKeyLayout compares the key layout of the Talos YAML with that of the field the typed document stores.
func checkKeyLayout(t *testing.T, kind string, talosYAML, stored []byte) {
	t.Helper()

	var fromYAML any
	if err := yaml.Unmarshal(talosYAML, &fromYAML); err != nil {
		t.Fatalf("reading %s's YAML: %v", kind, err)
	}

	if m, ok := fromYAML.(map[string]any); ok {
		delete(m, "kind") // the typed document's own discriminator
		delete(m, "apiVersion")
	}

	var envelope map[string]any
	if err := json.Unmarshal(stored, &envelope); err != nil {
		t.Fatalf("reading %s's JSON: %v", kind, err)
	}

	key, _ := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(kind)

	if a, b := layout(fromYAML), layout(envelope[key]); a != b {
		t.Errorf("%s: the mirror's JSON has a different key layout from Talos's YAML.\n--- YAML ---\n%s\n--- JSON ---\n%s", kind, a, b)
	}
}

// layout renders the nesting of the keys in v, with the shape of a list's first element, and no values.
func layout(v any) string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ":" + layout(x[k])
		}

		return "{" + strings.Join(parts, ",") + "}"
	case []any:
		// A list of scalars is not told from a scalar: the mirror writes meta.ArgValue as a list where Talos
		// writes a single value as a scalar.
		if len(x) == 0 || layout(x[0]) == "_" {
			return "_"
		}

		return "[" + layout(x[0]) + "]"
	default:
		return "_"
	}
}

// filler gives every field of a machinery document that its mirror carries a distinct non-zero value.
type filler struct {
	t *testing.T
	n int
}

// mirrorPkg is the package whose struct types are mirrors; a field whose mirror is any other struct is mirrored
// as an upstream type instead and is not filled.
var mirrorPkg = reflect.TypeFor[typedconfig.SysctlConfigV1Alpha1]().PkgPath()

var (
	metaType         = reflect.TypeFor[mmeta.Meta]()
	unstructuredType = reflect.TypeFor[mmeta.Unstructured]()
	textUnmarshaler  = reflect.TypeFor[encoding.TextUnmarshaler]()
	textMarshaler    = reflect.TypeFor[encoding.TextMarshaler]()
)

// scalarSamples are YAML scalars tried, in order, for a type that parses its own text (an address, a size, a
// duration, an enum). The first that the type accepts and writes back non-empty is used.
var scalarSamples = []string{
	"10.0.0.1", "10.0.0.0/24", "10.0.0.1:8080", "1h30m0s", "10GiB", "2 GB", "0o644", "https://example.com/path",
	"disk", "xfs", "luks2", "tls", "'>= 100 GB'", "volume.size > 1", "true",
}

func peel(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// fill fills v, a machinery value, where r is the type its mirror has (nil when there is none to consult).
func (f *filler) fill(v reflect.Value, r reflect.Type, depth int) {
	f.t.Helper()

	if depth > 12 || v.Type() == unstructuredType {
		return
	}

	r = peel(r)

	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		f.fill(v.Elem(), r, depth+1)

	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			v.SetBytes([]byte{1, 2, 3})

			return
		}

		var elem reflect.Type
		if r != nil && (r.Kind() == reflect.Slice) {
			elem = r.Elem()
		}

		v.Set(reflect.MakeSlice(v.Type(), 2, 2))

		for i := range 2 {
			f.fill(v.Index(i), elem, depth+1)
		}

	case reflect.Map:
		var elem reflect.Type
		if r != nil && r.Kind() == reflect.Map {
			elem = r.Elem()
		}

		v.Set(reflect.MakeMap(v.Type()))

		for range 2 {
			f.n++

			val := reflect.New(v.Type().Elem()).Elem()
			f.fill(val, elem, depth+1)
			v.SetMapIndex(reflect.ValueOf(fmt.Sprintf("k%d", f.n)).Convert(v.Type().Key()), val)
		}

	case reflect.Struct:
		if r != nil && r.Kind() == reflect.Struct && r.PkgPath() == mirrorPkg {
			f.fillStruct(v, r, depth)

			return
		}

		f.leaf(v)

	default:
		f.leaf(v)
	}
}

// fillStruct fills the fields of v that the mirror struct r also has, looking through an inline embedded struct
// the way the generator flattens it.
func (f *filler) fillStruct(v reflect.Value, r reflect.Type, depth int) {
	f.t.Helper()

	for i := range v.NumField() {
		sf := v.Type().Field(i)

		switch {
		case !sf.IsExported() || sf.Tag.Get("yaml") == "-" || sf.Type == metaType:
		case sf.Anonymous:
			f.fillStruct(v.Field(i), r, depth+1)
		default:
			if mf, ok := r.FieldByName(sf.Name); ok { // not in the mirror: dropped on purpose
				f.fill(v.Field(i), mf.Type, depth+1)
			}
		}
	}
}

// leaf sets a value with no mirrored structure: a number, a string, or a type that parses its own text.
func (f *filler) leaf(v reflect.Value) {
	f.t.Helper()

	f.n++

	p := reflect.PointerTo(v.Type())

	if v.Kind() == reflect.Struct || p.Implements(textUnmarshaler) || hasMethod(p, "UnmarshalYAML") {
		f.parsed(v)

		return
	}

	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.String:
		v.SetString(fmt.Sprintf("s%d", f.n))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(f.n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(f.n))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(f.n))
	default:
		f.t.Fatalf("no sample for %s: teach filler.leaf about it", v.Type())
	}
}

func hasMethod(t reflect.Type, name string) bool {
	_, ok := t.MethodByName(name)

	return ok
}

// parsed sets v to the first of scalarSamples (or, for an enum of integers, the first number) it accepts.
func (f *filler) parsed(v reflect.Value) {
	f.t.Helper()

	for _, s := range scalarSamples {
		x := reflect.New(v.Type())
		if yaml.Unmarshal([]byte(s), x.Interface()) != nil || x.Elem().IsZero() {
			continue
		}

		if out, err := yaml.Marshal(x.Interface()); err != nil || strings.TrimSpace(string(out)) == "" {
			continue
		}

		v.Set(x.Elem())

		return
	}

	if (v.CanInt() || v.CanUint()) && v.Type().Implements(textMarshaler) {
		for i := int64(1); i < 16; i++ {
			if v.CanInt() {
				v.SetInt(i)
			} else {
				v.SetUint(uint64(i))
			}

			if text, err := v.Interface().(encoding.TextMarshaler).MarshalText(); err == nil && len(text) > 0 && !strings.Contains(string(text), "unknown") {
				return
			}
		}
	}

	f.t.Fatalf("no sample for %s: add a scalar that it parses to scalarSamples", v.Type())
}
