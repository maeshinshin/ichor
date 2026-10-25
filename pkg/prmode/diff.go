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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Diff renders a unified diff from oldYAML to newYAML, for both
// SubmitPR's own PR description (the changed CR's before/after) and the
// "diff to apply by hand" architecture.md requires for Helm and Jsonnet
// sources (SourceHelm/SourcePlugin, which have no single file for
// Locate/Patch to find or edit at all).
//
// This shells out to `git diff --no-index` rather than adding a diff
// library: this package already depends on the `git` binary for
// Clone/CommitAndPush (see git.go), so a unified diff is already free,
// and a Go diff library would be the only thing in the module graph
// needing it (confirmed: `go mod why -m <pure-Go diff libraries
// considered>` showed no other caller before this function existed).
// `git diff --no-index` needs no repository at all -- it compares two
// plain files -- so no auth, no clone, nothing this function does can
// touch a real host.
func Diff(oldYAML, newYAML, name string) (string, error) {
	dir, err := os.MkdirTemp("", "ichor-prmode-diff-*")
	if err != nil {
		return "", fmt.Errorf("prmode: creating a diff scratch directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	// a/<name> and b/<name> (git's own convention for a two-sided diff)
	// so the rendered header reads "diff --git a/<name> b/<name>"
	// instead of an opaque temp path.
	oldRel, newRel := filepath.Join("a", name), filepath.Join("b", name)

	if err := writeScratchFile(dir, oldRel, oldYAML); err != nil {
		return "", err
	}

	if err := writeScratchFile(dir, newRel, newYAML); err != nil {
		return "", err
	}

	cmd := exec.Command("git", "diff", "--no-index", "--", oldRel, newRel)
	cmd.Dir = dir

	out, err := cmd.Output()

	var exitErr *exec.ExitError
	// git diff --no-index's own exit codes: 0 no difference, 1 a
	// difference was found (not a failure -- the whole point of calling
	// it), >1 (or no ExitError at all, e.g. git missing) a real error.
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != 1) {
		return "", fmt.Errorf("prmode: git diff --no-index: %w", err)
	}

	return string(out), nil
}

// writeScratchFile writes content to relPath under dir, creating
// relPath's own parent directories first (name may itself contain
// slashes, e.g. "clusters/prod/cluster.yaml").
func writeScratchFile(dir, relPath, content string) error {
	path := filepath.Join(dir, relPath)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("prmode: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("prmode: writing %s: %w", path, err)
	}

	return nil
}
