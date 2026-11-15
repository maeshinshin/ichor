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

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// ichorTag is the struct tag pkg/ichorconfig carries on every setting: where the setting goes in a Talos
// document. It is written by internal/tools/ichorgen. A setting names its sinks, "Kind:path;Kind:path", the
// kind of a Talos document ("Config" is the v1alpha1 document) and the dotted JSON path inside it. A
// collection of named documents says "docs=Kind", and each entry is one document of that kind whose sinks are
// paths inside that document. A sink "+Kind:" says the setting being true brings the document of that kind
// into being, for a document whose presence is the setting.
const ichorTag = "ichor"

// configKind is the kind of the v1alpha1 document in a TalosConfigPatchDocument.
const configKind = "Config"

// EffectiveDocuments returns the Talos documents this patch carries: its spec.documents, or spec.config converted. A
// patch that sets both is refused, because the same setting could then be written twice. multidoc says
// whether the machines the patch applies to take the documents Talos 1.14 added for the Kubernetes
// components; when they do not, those settings are written into the v1alpha1 document instead.
func (s *TalosConfigPatchSpec) EffectiveDocuments(multidoc bool) ([]TalosConfigPatchDocument, error) {
	if len(s.Documents) > 0 && s.Config != nil {
		return nil, ErrDocumentsAndConfig
	}

	if s.Config == nil {
		return s.Documents, nil
	}

	return ConvertConfig(s.Config, multidoc)
}

// ErrDocumentsAndConfig is the refusal of a patch that sets spec.documents and spec.config.
var ErrDocumentsAndConfig = fmt.Errorf("spec.documents and spec.config are both set: a patch sets one of them, because " +
	"the same setting could be written in both; spec.documents is deprecated, move its settings to spec.config")

type instance struct {
	kind, name string
}

type converter struct {
	multidoc bool
	trees    map[instance]map[string]any
}

// ConvertConfig converts Ichor's configuration to the Talos documents that carry it, one for each document
// the settings reach. Every setting of the schema has the sinks its tag names; a setting a document of its own
// spells goes to that document, and one only the v1alpha1 document has goes there.
func ConvertConfig(c *ichorconfig.Config, multidoc bool) ([]TalosConfigPatchDocument, error) {
	cv := &converter{multidoc: multidoc, trees: map[instance]map[string]any{}}

	if err := cv.walk(reflect.ValueOf(c).Elem(), map[string]instance{}); err != nil {
		return nil, err
	}

	keys := make([]instance, 0, len(cv.trees))
	for k := range cv.trees {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, func(a, b instance) int {
		if c := strings.Compare(a.kind, b.kind); c != 0 {
			return c
		}

		return strings.Compare(a.name, b.name)
	})

	docs := make([]TalosConfigPatchDocument, 0, len(keys))

	for _, k := range keys {
		d, err := cv.document(k)
		if err != nil {
			return nil, err
		}

		docs = append(docs, d)
	}

	return docs, nil
}

// document builds the union entry of one instance by decoding its tree into the mirror type of its kind, the
// same type a spec.documents entry of that kind is read into, so that nothing the mirror does not carry passes.
func (cv *converter) document(k instance) (TalosConfigPatchDocument, error) {
	key, ok := TalosConfigPatchDocumentFieldKey(k.kind)
	if !ok {
		return TalosConfigPatchDocument{}, fmt.Errorf("spec.config: no document kind %q", k.kind)
	}

	raw, err := json.Marshal(map[string]any{"kind": k.kind, key: cv.trees[k]})
	if err != nil {
		return TalosConfigPatchDocument{}, err
	}

	var d TalosConfigPatchDocument

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&d); err != nil {
		return TalosConfigPatchDocument{}, fmt.Errorf("spec.config: the %s document: %w", describe(k), err)
	}

	if err := d.Validate(); err != nil {
		return TalosConfigPatchDocument{}, fmt.Errorf("spec.config: %w", err)
	}

	return d, nil
}

func describe(k instance) string {
	if k.name == "" {
		return k.kind
	}

	return k.kind + " " + k.name
}

// sinkOf is one place a setting goes.
type sinkOf struct {
	kind, path string
	presence   bool
}

func parseSinks(tag string) []sinkOf {
	var out []sinkOf

	for _, part := range strings.Split(tag, ";") {
		kind, path, _ := strings.Cut(part, ":")

		s := sinkOf{kind: kind, path: path}
		if after, ok := strings.CutPrefix(kind, "+"); ok {
			s.kind, s.presence = after, true
		}

		out = append(out, s)
	}

	return out
}

// choose picks the sinks a setting is written to. A document that spells the setting wins over the v1alpha1
// document, which cannot hold the same setting as a document of its own: Talos refuses both. Where the machine
// does not take the Kubernetes documents, the v1alpha1 spelling is the only one.
func (cv *converter) choose(sinks []sinkOf, where string, v reflect.Value) ([]sinkOf, error) {
	var docs, cfg, pres []sinkOf

	for _, s := range sinks {
		switch {
		case s.presence:
			pres = append(pres, s)
		case s.kind == configKind:
			cfg = append(cfg, s)
		case !cv.multidoc && TalosConfigPatchDocumentIsKubernetes(s.kind):
			// not available here
		default:
			docs = append(docs, s)
		}
	}

	if len(pres) > 0 && isTrue(v) && cv.multidoc {
		return pres, nil
	}

	if len(docs) > 0 {
		return docs, nil
	}

	if len(cfg) > 0 {
		return cfg, nil
	}

	return nil, fmt.Errorf("spec.config.%s needs a machine on a Talos version that takes the %s document (Talos 1.14 and later)", where, sinks[0].kind)
}

func isTrue(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}

		v = v.Elem()
	}

	return v.Kind() == reflect.Bool && v.Bool()
}

func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	}

	return v.IsZero()
}

func isSchema(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t.Kind() == reflect.Struct && t.PkgPath() == reflect.TypeFor[ichorconfig.Config]().PkgPath()
}

// walk visits the settings of one object of the schema. entries holds, for each kind of document the object is
// an entry of, the instance of it, which settings inside the entry write to by their relative path.
func (cv *converter) walk(v reflect.Value, entries map[string]instance) error {
	return cv.walkAt(v, entries, "")
}

func (cv *converter) walkAt(v reflect.Value, entries map[string]instance, at string) error {
	t := v.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		fv := v.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		where := strings.TrimPrefix(at+"."+name, ".")
		tag := f.Tag.Get(ichorTag)

		if isEmpty(fv) {
			continue
		}

		switch {
		case strings.HasPrefix(tag, "docs="):
			if err := cv.entries(fv, strings.TrimPrefix(tag, "docs="), where); err != nil {
				return err
			}
		case isSchema(f.Type):
			child := fv
			for child.Kind() == reflect.Pointer {
				child = child.Elem()
			}

			if err := cv.walkAt(child, entries, where); err != nil {
				return err
			}
		default:
			if tag == "" {
				return fmt.Errorf("spec.config.%s has no destination in a Talos document", where)
			}

			sinks, err := cv.choose(parseSinks(tag), where, fv)
			if err != nil {
				return err
			}

			if err := cv.write(sinks, entries, fv, where); err != nil {
				return err
			}
		}
	}

	return nil
}

// entries visits a collection of named documents: each entry is one document of kind, named by its key (or,
// for a list, by the name inside the entry).
func (cv *converter) entries(coll reflect.Value, kind, where string) error {
	visit := func(name string, item reflect.Value, at string) error {
		for item.Kind() == reflect.Pointer {
			if item.IsNil() {
				return nil
			}

			item = item.Elem()
		}

		inst := instance{kind, name}
		tree := cv.tree(inst)

		if coll.Kind() == reflect.Map {
			tree["name"] = name
		}

		return cv.walkAt(item, map[string]instance{kind: inst}, at)
	}

	if coll.Kind() == reflect.Map {
		for _, k := range slices.Sorted(func(yield func(string) bool) {
			for _, mk := range coll.MapKeys() {
				if !yield(mk.String()) {
					return
				}
			}
		}) {
			if err := visit(k, coll.MapIndex(reflect.ValueOf(k)), where+"."+k); err != nil {
				return err
			}
		}

		return nil
	}

	for i := range coll.Len() {
		if err := visit(strconv.Itoa(i), coll.Index(i), fmt.Sprintf("%s[%d]", where, i)); err != nil {
			return err
		}
	}

	return nil
}

func (cv *converter) tree(i instance) map[string]any {
	t, ok := cv.trees[i]
	if !ok {
		t = map[string]any{}
		cv.trees[i] = t
	}

	return t
}

// write puts the value of a setting at each of its sinks.
func (cv *converter) write(sinks []sinkOf, entries map[string]instance, v reflect.Value, where string) error {
	raw, err := json.Marshal(v.Interface())
	if err != nil {
		return fmt.Errorf("spec.config.%s: %w", where, err)
	}

	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return err
	}

	for _, s := range sinks {
		inst, ok := entries[s.kind]
		if !ok {
			inst = instance{kind: s.kind}
		}

		tree := cv.tree(inst)

		if s.presence {
			continue
		}

		setPath(tree, s.path, val)
	}

	return nil
}

func setPath(tree map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		next, ok := tree[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			tree[p] = next
		}

		tree = next
	}

	tree[parts[len(parts)-1]] = val
}
