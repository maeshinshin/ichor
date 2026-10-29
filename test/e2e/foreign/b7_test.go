//go:build e2e
// +build e2e

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

package foreign

import (
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/test/internal/foreign"
)

// TestB7ClassC (B7): 3 control planes and a worker whose machine config carries a user inline manifest of a non-Cilium app.
// Cluster: `foreign up --cps 3 --workers 1 --user-manifest`. Every machine is class C, so adopt writes nothing and applies nothing.
func TestB7ClassC(t *testing.T) {
	c := build(t, foreign.Options{ControlPlanes: 3, Workers: 1, UserManifest: true})
	before := cniPaths(t, c)
	for _, extra := range []string{"--plan", "--yes"} {
		out, err := adoptArgs(t, c, append(recoverFlags(c), "--cni-unmanaged", extra)...)
		if err == nil {
			t.Errorf("adopt %s succeeded on a cluster with a user inline manifest", extra)
		}
		for _, want := range []string{"KubeInlineManifestConfig.manifest", "nothing was written"} {
			if !strings.Contains(out, want) {
				t.Errorf("adopt %s output lacks %q", extra, want)
			}
		}
	}
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
}
