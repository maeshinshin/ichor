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
	"slices"
	"strings"
	"testing"
)

const (
	mirrorLegacyYAML = `version: v1alpha1
machine:
  type: worker
  registries:
    mirrors:
      ghcr.io:
        endpoints:
          - http://10.0.0.1:5000
        overridePath: true
        skipFallback: true
    config:
      ghcr.io:
        auth:
          username: u
`
	mirrorDocYAML = `version: v1alpha1
machine:
  type: worker
  registries:
    config:
      ghcr.io:
        auth:
          username: u
---
apiVersion: v1alpha1
kind: RegistryMirrorConfig
name: ghcr.io
endpoints:
  - url: http://10.0.0.1:5000
    overridePath: true
skipFallback: true
`
)

func TestChangedPaths_RegistryMirrorFormsAreEquivalent(t *testing.T) {
	for _, tc := range [][2]string{{mirrorLegacyYAML, mirrorDocYAML}, {mirrorDocYAML, mirrorLegacyYAML}} {
		paths, err := ChangedPaths([]byte(tc[0]), []byte(tc[1]))
		if err != nil {
			t.Fatalf("ChangedPaths: %v", err)
		}

		if len(paths) != 0 {
			t.Fatalf("same mirrors in two forms drifted: %v", paths)
		}
	}
}

func TestChangedPaths_RegistryMirrorRealDifferencesStayDrift(t *testing.T) {
	cases := map[string]struct{ doc, want string }{
		"endpoints":    {strings.Replace(mirrorDocYAML, "10.0.0.1", "10.0.0.2", 1), "RegistryMirrorConfig.endpoints"},
		"skipFallback": {strings.Replace(mirrorDocYAML, "skipFallback: true", "", 1), "RegistryMirrorConfig.skipFallback"},
		// Credentials in machine.registries.config are not touched by the normalisation.
		"credentials": {
			strings.Replace(mirrorDocYAML, "username: u", "username: v", 1),
			"machine.registries.config.ghcr.io.auth.username",
		},
	}

	for name, tc := range cases {
		paths, err := ChangedPaths([]byte(mirrorLegacyYAML), []byte(tc.doc))
		if err != nil {
			t.Fatalf("%s: ChangedPaths: %v", name, err)
		}

		if !slices.Contains(paths, tc.want) {
			t.Fatalf("%s: want %q in %v", name, tc.want, paths)
		}
	}
}
