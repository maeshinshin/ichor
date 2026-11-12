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

package main

import (
	"fmt"
	"maps"
	"slices"
)

// droppedPayloads records the decision for every meta.Unstructured field a shipped kind loses. The mirror
// cannot carry such a field (it is free-form YAML, and carrying it would be the raw-YAML escape hatch), so a
// document that sets it is accepted with that content gone. That is acceptable only where the field is a
// side detail of a kind that is useful without it, and only when someone has said so: a shipped kind whose
// mirror has no field at all, or that loses an Unstructured field with no row here, stops generation and
// fails TestShippedKindsKeepTheirPayload. Keyed "machinery struct.Go field" like secretFields.
var droppedPayloads = map[string]string{
	"KubeletConfig.KubeletExtraConfig":              "free-form kubelet configuration; the typed kubelet fields of the legacy document are mirrored",
	"MachineConfig.MachinePods":                     "static pod manifests: mirroring them means mirroring the Kubernetes Pod type, which is large (hundreds of KB of schema) and held for a separate decision",
	"MachineConfig.MachineBaseRuntimeSpecOverrides": "free-form OCI runtime spec override; no single schema",
	"AdmissionPluginConfig.PluginConfiguration":     "free-form per-plugin admission configuration; the plugin name is mirrored",
	"SchedulerConfig.SchedulerConfig":               "free-form kube-scheduler configuration; the legacy document's other scheduler fields are mirrored",
}

// reachable returns d and every struct reachable from it through its fields, each once.
func (d *structDef) reachable() []*structDef {
	seen := map[*structDef]bool{}

	var out []*structDef

	var visit func(*structDef)

	visit = func(s *structDef) {
		if seen[s] {
			return
		}

		seen[s] = true
		out = append(out, s)

		for _, f := range s.fields {
			for m := f.mapping; m != nil; m = m.elem {
				if m.structDef != nil {
					visit(m.structDef)
				}
			}
		}
	}

	visit(d)

	return out
}

// payloadProblems lists, by kind and field, every shipped kind that would be accepted with its content
// discarded: a root whose mirror has no fields, a meta.Unstructured field dropped with no row in decisions,
// and a row in decisions that no shipped kind hits any more (so the table cannot rot).
func (g *generator) payloadProblems(decisions map[string]string) []string {
	var problems []string

	hit := map[string]bool{}

	for _, def := range g.order {
		if !def.isRoot {
			continue
		}

		if len(def.fields) == 0 {
			problems = append(problems, fmt.Sprintf("kind %s: its mirror %s has no fields, so every document of it is accepted empty", def.rootKind.kind, def.name))
		}

		for _, s := range def.reachable() {
			for _, d := range s.dropped {
				hit[d.Key] = true

				if _, ok := decisions[d.Key]; !ok {
					problems = append(problems, fmt.Sprintf("kind %s: %s (yaml %q) is a meta.Unstructured field the mirror drops, and droppedPayloads has no row %q", def.rootKind.kind, d.Key, d.YAML, d.Key))
				}
			}
		}
	}

	for _, k := range slices.Sorted(maps.Keys(decisions)) {
		if !hit[k] {
			problems = append(problems, fmt.Sprintf("droppedPayloads row %q matches no dropped field of a shipped kind: remove it", k))
		}
	}

	return slices.Compact(slices.Sorted(slices.Values(problems)))
}
