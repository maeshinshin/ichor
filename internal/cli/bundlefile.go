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
	"fmt"
	"os"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
)

// loadBundleFile reads the cluster's secrets bundle from a local file,
// encoded the same way `talosctl gen secrets` writes it and the way
// internal/controller/bundle.go reads it back out of a Secret (the key
// "secrets.yaml" that convention describes). adopt predates Ichor's own
// management of the cluster (ADR 0014): there is no Secret to read it from
// yet, so it comes from wherever the operator already keeps it, the same
// file `ichor init` would have written to ~/.ichor/clusters/<name>/.
//
// Never wrap a decoding error with the raw bytes or the decoded value --
// only path and structural detail -- so a malformed bundle file can't put
// its own secret material into an error message (pkg/render's leak test
// follows the same rule).
func loadBundleFile(path string) (*secrets.Bundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading secrets bundle %q: %w", path, err)
	}

	var bundle secrets.Bundle
	if err := yaml.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decoding secrets bundle %q: %w", path, err)
	}

	return &bundle, nil
}
