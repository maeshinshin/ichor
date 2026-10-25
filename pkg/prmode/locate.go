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
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Locate finds the file holding target within root/sourcePath -- the
// directory an already-checked-out copy of a SourceFile Application's
// repository, narrowed to its spec.source.path, would occupy. It never
// clones or fetches anything itself; producing that checkout is the
// repository-connection plumbing a later, host-specific unit owns (see
// package doc comment).
//
// Every ".yaml"/".yml" file under the directory is read in sorted order
// (so which file wins when more than one somehow matches is
// deterministic, not directory-iteration order) and split into its
// `---`-delimited documents; the first file containing a document whose
// apiVersion/kind/metadata.name/metadata.namespace equal target is
// returned. If none does, Locate fails loudly rather than returning an
// empty path -- a target CR missing from its own Application's source
// is exactly the surprising case a silent no-op would hide.
func Locate(root, sourcePath string, target Target) (string, error) {
	dir := filepath.Join(root, sourcePath)

	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("prmode: locating %s: %w", dir, err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("prmode: %s is not a directory", dir)
	}

	var files []string

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("prmode: walking %s: %w", dir, err)
	}

	slices.Sort(files)

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("prmode: reading %s: %w", path, err)
		}

		for _, seg := range splitDocuments(data) {
			doc, err := decodeDocument(seg.content)
			if err != nil {
				return "", fmt.Errorf("prmode: %s: %w", path, err)
			}

			if doc == nil {
				continue
			}

			if matches(doc, target) {
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("prmode: no file under %s declares %s %s (namespace %s, name %s)",
		dir, target.APIVersion, target.Kind, target.Namespace, target.Name)
}
