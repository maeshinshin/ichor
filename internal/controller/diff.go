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

package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"

	yaml "go.yaml.in/yaml/v4"
)

// changedPaths implements design step 3 (configuration reconcile): it
// reports which field paths differ between a node's current configuration
// and the freshly rendered one, spelled the way pkg/fieldmeta expects --
// dot-separated, rooted at "machine"/"cluster" for the legacy v1alpha1
// Config document and at the kind name for a standalone document such as
// SysctlConfig or KernelModuleConfig (fieldmeta.go's own doc comment).
//
// It works generically off the decoded YAML tree rather than machinery's
// typed config structs: a document's "kind" field (absent for the legacy
// Config document) and, for named documents, its "name" field are the only
// things it needs to know, and both are Talos's own document discriminators
// (config.NamedDocument), not something this function invents.
func changedPaths(oldCfg, newCfg []byte) ([]string, error) {
	oldDocs, err := decodeDocs(oldCfg)
	if err != nil {
		return nil, fmt.Errorf("decoding current configuration: %w", err)
	}

	newDocs, err := decodeDocs(newCfg)
	if err != nil {
		return nil, fmt.Errorf("decoding rendered configuration: %w", err)
	}

	keys := make(map[string]struct{}, len(oldDocs)+len(newDocs))
	for k := range oldDocs {
		keys[k] = struct{}{}
	}

	for k := range newDocs {
		keys[k] = struct{}{}
	}

	var paths []string

	for k := range keys {
		o, oOK := oldDocs[k]
		n, nOK := newDocs[k]

		root := o.root
		if !oOK {
			root = n.root
		}

		var oldData, newData any
		if oOK {
			oldData = o.data
		}

		if nOK {
			newData = n.data
		}

		diffPaths(root, oldData, newData, &paths)
	}

	slices.Sort(paths)

	return paths, nil
}

// configDoc is one decoded Talos config document.
type configDoc struct {
	// root is the fieldmeta path prefix for this document: "" for the
	// legacy Config document, otherwise its kind.
	root string
	data map[string]any
}

// decodeDocs splits b (a "---"-separated multi-document YAML stream, the
// shape pkg/render and machinery's own container encoder both produce) into
// one configDoc per document, keyed so that old and new documents of the
// same kind -- and, for named documents, the same name -- line up with each
// other even when other documents were added or removed around them.
func decodeDocs(b []byte) (map[string]configDoc, error) {
	docs := make(map[string]configDoc)
	dec := yaml.NewDecoder(bytes.NewReader(b))

	for {
		var m map[string]any

		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		if m == nil {
			continue
		}

		kind, _ := m["kind"].(string)
		if kind == "" {
			// The legacy v1alpha1 Config document carries no "kind" field
			// and is always a singleton.
			docs[""] = configDoc{root: "", data: m}

			continue
		}

		name, _ := m["name"].(string)
		docs[kind+":"+name] = configDoc{root: kind, data: m}
	}

	return docs, nil
}

// diffPaths walks oldV and newV together, appending prefix-rooted dot paths
// to out for every leaf where they differ. Maps are recursed into key by
// key, on the union of keys from both sides, so that a key present on only
// one side is reported as changed down to its own leaves rather than as a
// single opaque difference at the parent. Anything else (scalars, lists) is
// compared as a whole.
func diffPaths(prefix string, oldV, newV any, out *[]string) {
	oldMap, oldIsMap := oldV.(map[string]any)
	newMap, newIsMap := newV.(map[string]any)

	if oldIsMap || newIsMap {
		keys := make(map[string]struct{}, len(oldMap)+len(newMap))
		for k := range oldMap {
			keys[k] = struct{}{}
		}

		for k := range newMap {
			keys[k] = struct{}{}
		}

		for k := range keys {
			diffPaths(joinPath(prefix, k), oldMap[k], newMap[k], out)
		}

		return
	}

	if !reflect.DeepEqual(oldV, newV) {
		*out = append(*out, prefix)
	}
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}

	return prefix + "." + key
}
