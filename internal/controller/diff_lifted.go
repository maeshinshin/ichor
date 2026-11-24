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

	trees := map[[2]string]map[string]any{}

	for _, d := range docs {
		if d.root != "" {
			name, _ := d.data[labelName].(string)
			trees[[2]string{d.root, name}] = d.data
		}

	}

	root := v1.data

	if err := ichorv1alpha1.LiftSettings(root, trees); err != nil {
		return
	}

	for k, t := range trees {
		key := k[0] + ":" + k[1]
		if _, exists := docs[key]; exists {
			continue
		}

		t["apiVersion"], t["kind"] = "v1alpha1", k[0]
		docs[key] = configDoc{root: k[0], data: t}
	}
}
