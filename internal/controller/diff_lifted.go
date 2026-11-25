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
	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// normalizeLiftedSettings rewrites the settings the v1alpha1 document spells that Talos 1.14 also spells in a
// document of their own into that document, as normalizeRegistryMirrors does for registry mirrors. The move table is
// the one the TalosConfigPatch lift uses (ichorv1alpha1.LiftSettings), so the two cannot drift apart. Talos folds
// both forms into one resource (for time: timeserver_config.go in v1.14.5 reads NetworkTimeSyncConfig(), whichever
// form set it), so a node an earlier build configured in the v1alpha1 form holds the same setting as the rendered
// document: no change, no reboot. A field the document already sets makes the move fail, and the settings stay as
// they were, which the diff then reports.
func normalizeLiftedSettings(docs map[string]configDoc) {
	v1, ok := docs[""]
	if !ok {
		return
	}

	// The move works on copies: on an error it may have half moved the trees, and the settings must stay as they
	// were.
	trees := map[[2]string]map[string]any{}

	for _, d := range docs {
		if d.root != "" {
			name, _ := d.data[labelName].(string)
			trees[[2]string{d.root, name}] = deepCopyTree(d.data)
		}
	}

	root := deepCopyTree(v1.data)

	if err := ichorv1alpha1.LiftSettings(root, trees); err != nil {
		return
	}

	docs[""] = configDoc{root: "", data: root}

	for k, t := range trees {
		key := k[0] + ":" + k[1]
		if old, exists := docs[key]; exists {
			docs[key] = configDoc{root: old.root, data: t}

			continue
		}

		t["apiVersion"], t["kind"] = "v1alpha1", k[0]
		docs[key] = configDoc{root: k[0], data: t}
	}
}

// deepCopyTree copies the maps and lists of a decoded YAML tree (its scalars are values).
func deepCopyTree(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))

	for k, v := range m {
		out[k] = deepCopyValue(v)
	}

	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyTree(t)
	case []any:
		out := make([]any, len(t))

		for i, e := range t {
			out[i] = deepCopyValue(e)
		}

		return out
	}

	return v
}
