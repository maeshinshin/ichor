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

package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// writeOutput prints patches to w in the requested format
// (docs/design/architecture.md, "CLI and UI": "-o yaml|json"). Both formats
// marshal the same ichorv1alpha1.TalosConfigPatch values adopt would create
// -- never the secrets bundle, never a rendered machine configuration --
// so there is nothing sensitive for either encoder to reach.
func writeOutput(w io.Writer, format string, patches []ichorv1alpha1.TalosConfigPatch) error {
	return writeObjects(w, format, "adopt", patches)
}

// writeOutputObjects is writeOutput's init counterpart: init creates a TalosCluster, a
// TalosMachine and zero or more TalosConfigPatches, so this takes whatever client.Object values
// the caller built. Same two formats, same rule: every object printed is a CR init is creating or
// just created, never the secrets bundle or a rendered machine configuration.
func writeOutputObjects(w io.Writer, format string, objs []client.Object) error {
	return writeObjects(w, format, "init", objs)
}

// writeObjects prints objs as one JSON array or as YAML documents split by "---"; who prefixes the errors.
func writeObjects[T any](w io.Writer, format, who string, objs []T) error {
	switch machineFormat(format) {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(objs); err != nil {
			return fmt.Errorf("%s: encoding output as JSON: %w", who, err)
		}

		return nil
	case outputYAML:
		for i := range objs {
			if i > 0 {
				if _, err := fmt.Fprintln(w, "---"); err != nil {
					return err
				}
			}

			out, err := sigsyaml.Marshal(&objs[i])
			if err != nil {
				return fmt.Errorf("%s: encoding output as YAML: %w", who, err)
			}

			if _, err := w.Write(out); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("%s: invalid output format %q", who, format)
	}
}
