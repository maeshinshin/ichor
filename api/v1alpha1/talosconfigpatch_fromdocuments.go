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
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// ConfigFromDocuments is ConvertConfig's inverse: it writes the settings of Talos documents in Ichor's
// schema, for what builds documents (the CLI's init and adopt, the UI's document editor). A document that holds
// a setting the schema does not carry is refused, naming it, rather than losing it: the result is converted
// back and must give the documents it was made from.
func ConfigFromDocuments(docs []TalosConfigPatchDocument) (*ichorconfig.Config, error) {
	trees := map[instance]map[string]any{}
	order := map[string][]instance{}

	for _, d := range docs {
		key, ok := TalosConfigPatchDocumentFieldKey(d.Kind)
		if !ok {
			return nil, fmt.Errorf("unknown document kind %q", d.Kind)
		}

		tree, err := documentTree(d, key)
		if err != nil {
			return nil, err
		}

		name, _ := tree["name"].(string)
		if slices.Contains(order[d.Kind], instance{d.Kind, name}) && name != "" {
			return nil, fmt.Errorf("two %s documents are named %q", d.Kind, name)
		}

		inst := instance{d.Kind, name}
		trees[inst] = tree
		order[d.Kind] = append(order[d.Kind], inst)
	}

	c := &ichorconfig.Config{}
	r := &reverser{trees: trees, order: order}

	if _, err := r.fill(reflect.ValueOf(c).Elem(), nil); err != nil {
		return nil, err
	}

	back, err := ConvertConfig(c, true)
	if err != nil {
		return nil, err
	}

	if lost := lostSettings(docs, back); len(lost) > 0 {
		return nil, fmt.Errorf("spec.config does not carry %s: Ichor's schema writes each setting once, in the "+
			"spelling of the document that has its own; write it there", strings.Join(lost, ", "))
	}

	return c, nil
}

func documentTree(d TalosConfigPatchDocument, key string) (map[string]any, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}

	tree, _ := env[key].(map[string]any)
	if tree == nil {
		tree = map[string]any{}
	}

	return tree, nil
}

type reverser struct {
	trees map[instance]map[string]any
	// order is the instances of each kind, in the order the documents came.
	order map[string][]instance
}

// fill sets the fields of one object of the schema from the documents; it reports whether it set any.
// entries holds, for the kind of document the object is an entry of, that document's instance.
func (r *reverser) fill(v reflect.Value, entries map[string]instance) (bool, error) {
	t := v.Type()
	set := false

	for i := range t.NumField() {
		f, fv := t.Field(i), v.Field(i)
		tag := f.Tag.Get(ichorTag)

		switch {
		case strings.HasPrefix(tag, "docs="):
			ok, err := r.fillEntries(fv, strings.TrimPrefix(tag, "docs="))
			if err != nil {
				return false, err
			}

			set = set || ok
		case isSchema(f.Type):
			child := reflect.New(f.Type.Elem())

			ok, err := r.fill(child.Elem(), entries)
			if err != nil {
				return false, err
			}

			if ok {
				fv.Set(child)
			}

			set = set || ok
		default:
			ok, err := r.fillLeaf(fv, tag, entries)
			if err != nil {
				return false, err
			}

			set = set || ok
		}
	}

	return set, nil
}

func (r *reverser) fillEntries(coll reflect.Value, kind string) (bool, error) {
	insts := r.order[kind]
	if len(insts) == 0 {
		return false, nil
	}

	if coll.Kind() == reflect.Map {
		coll.Set(reflect.MakeMap(coll.Type()))
	}

	for _, inst := range insts {
		item := reflect.New(coll.Type().Elem().Elem())

		if _, err := r.fill(item.Elem(), map[string]instance{kind: inst}); err != nil {
			return false, err
		}

		if coll.Kind() == reflect.Map {
			coll.SetMapIndex(reflect.ValueOf(inst.name), item)
		} else {
			coll.Set(reflect.Append(coll, item))
		}
	}

	return true, nil
}

func (r *reverser) fillLeaf(fv reflect.Value, tag string, entries map[string]instance) (bool, error) {
	for _, s := range parseSinks(tag) {
		inst, ok := entries[s.kind]
		if !ok {
			inst = instance{kind: s.kind}
		}

		tree, ok := r.trees[inst]
		if !ok {
			continue
		}

		if s.presence {
			if fv.Type().Elem().Kind() == reflect.Bool {
				b := true
				fv.Set(reflect.ValueOf(&b))

				return true, nil
			}

			continue
		}

		val := getPath(tree, s.path)
		if val == nil {
			continue
		}

		raw, err := json.Marshal(val)
		if err != nil {
			return false, err
		}

		dst := reflect.New(fv.Type())
		if err := json.Unmarshal(raw, dst.Interface()); err != nil {
			return false, fmt.Errorf("%s of %s: %w", s.path, describe(inst), err)
		}

		if isEmpty(dst.Elem()) {
			continue
		}

		fv.Set(dst.Elem())

		return true, nil
	}

	return false, nil
}

// lostSettings lists the settings of want that the documents of got do not give back.
func lostSettings(want, got []TalosConfigPatchDocument) []string {
	have := flatten(got)

	var lost []string

	for k, v := range flatten(want) {
		if !reflect.DeepEqual(have[k], v) {
			lost = append(lost, k)
		}
	}

	slices.Sort(lost)

	return lost
}

// flatten maps every setting of the documents, as "Kind[name] path", to its value.
func flatten(docs []TalosConfigPatchDocument) map[string]any {
	out := map[string]any{}

	for _, d := range docs {
		key, _ := TalosConfigPatchDocumentFieldKey(d.Kind)

		tree, err := documentTree(d, key)
		if err != nil {
			continue
		}

		// Ichor writes the version of the document it converts to.
		if d.Kind == configKind {
			delete(tree, "version")
		}

		name, _ := tree["name"].(string)
		head := d.Kind
		if name != "" {
			head += "[" + name + "]"
		}

		leaves(out, head, tree)
	}

	return out
}

func leaves(out map[string]any, at string, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			leaves(out, at+" "+k, c)
		}
	case []any:
		for i, c := range x {
			leaves(out, at+"["+strconv.Itoa(i)+"]", c)
		}
	case nil:
	default:
		out[at] = v
	}
}
