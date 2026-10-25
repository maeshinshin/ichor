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

	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// writeOutput prints patches to w in the requested format
// (docs/design/architecture.md, "CLI and UI": "-o yaml|json"). Both formats
// marshal the same ichorv1alpha1.TalosConfigPatch values adopt would create
// -- never the secrets bundle, never a rendered machine configuration --
// so there is nothing sensitive for either encoder to reach.
func writeOutput(w io.Writer, format string, patches []ichorv1alpha1.TalosConfigPatch) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if err := enc.Encode(patches); err != nil {
			return fmt.Errorf("adopt: encoding output as JSON: %w", err)
		}

		return nil
	case outputYAML:
		for i := range patches {
			if i > 0 {
				if _, err := fmt.Fprintln(w, "---"); err != nil {
					return err
				}
			}

			out, err := sigsyaml.Marshal(&patches[i])
			if err != nil {
				return fmt.Errorf("adopt: encoding output as YAML: %w", err)
			}

			if _, err := w.Write(out); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("adopt: invalid output format %q", format)
	}
}
