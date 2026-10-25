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

package prmode

import (
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff renders a unified diff from oldYAML to newYAML, for the "diff to
// apply by hand" architecture.md requires for Helm and Jsonnet sources
// (SourceHelm/SourcePlugin): there is no single file in the repository
// holding either CR's declared form (only rendered output, which this
// package never touches), so there is nothing for Locate/Patch to find
// or edit, and the caller falls back to this instead. oldYAML/newYAML
// are the caller's own concern -- typically the CR's current and
// about-to-be-applied rendering -- not anything this package reads from
// a file or a cluster.
func Diff(oldYAML, newYAML, name string) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(oldYAML),
		B:        difflib.SplitLines(newYAML),
		FromFile: name,
		ToFile:   name,
		Context:  3,
	}

	out, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", fmt.Errorf("prmode: computing diff: %w", err)
	}

	return out, nil
}
