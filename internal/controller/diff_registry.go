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

const registryMirrorKind = "RegistryMirrorConfig"

// normalizeRegistryMirrors rewrites the legacy machine.registries.mirrors of the v1alpha1 Config
// document into RegistryMirrorConfig documents, the form the render uses from contract v1.12, and
// drops the false flags a document may or may not spell. Talos folds both forms into one resource,
// so the same mirrors in either form are one configuration, not drift: a node an earlier Ichor
// build configured in the legacy form must stay Ready after an upgrade, and a v1.14 adopt of a
// document-form node must show no difference. machine.registries.config (credentials, TLS) is left
// alone. A host stated in both forms keeps its document; the legacy entry is dropped.
func normalizeRegistryMirrors(docs map[string]configDoc) {
	for _, d := range docs {
		if d.root == registryMirrorKind {
			normalizeMirrorDoc(d.data)
		}
	}

	legacy, ok := docs[""]
	if !ok {
		return
	}

	machine, _ := legacy.data["machine"].(map[string]any)
	registries, _ := machine["registries"].(map[string]any)
	mirrors, _ := registries["mirrors"].(map[string]any)

	if len(mirrors) == 0 {
		return
	}

	delete(registries, "mirrors")

	if len(registries) == 0 {
		delete(machine, "registries")
	}

	for host, v := range mirrors {
		key := registryMirrorKind + ":" + host
		if _, exists := docs[key]; exists {
			continue
		}

		m, _ := v.(map[string]any)
		override, _ := m["overridePath"].(bool)
		doc := map[string]any{"apiVersion": "v1alpha1", "kind": registryMirrorKind, "name": host}

		if eps, _ := m["endpoints"].([]any); len(eps) > 0 {
			out := make([]any, 0, len(eps))

			for _, ep := range eps {
				e := map[string]any{"url": ep}
				if override {
					e["overridePath"] = true
				}

				out = append(out, e)
			}

			doc["endpoints"] = out
		}

		if skip, _ := m["skipFallback"].(bool); skip {
			doc["skipFallback"] = true
		}

		docs[key] = configDoc{root: registryMirrorKind, data: doc}
	}
}

func normalizeMirrorDoc(data map[string]any) {
	if skip, ok := data["skipFallback"].(bool); ok && !skip {
		delete(data, "skipFallback")
	}

	eps, _ := data["endpoints"].([]any)
	for _, ep := range eps {
		if e, _ := ep.(map[string]any); e != nil {
			if o, ok := e["overridePath"].(bool); ok && !o {
				delete(e, "overridePath")
			}
		}
	}
}
