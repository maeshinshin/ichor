//go:build e2e

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

// TestB7ClassC (B7): 3 control planes and a worker whose machine config carries a user inline manifest of a
// non-Cilium app, written as a KubeInlineManifestConfig document. The document is representable, so adopt captures
// it into each machine's patch; the plan writes nothing.
func TestB7ClassC(t *testing.T) {
	c := build(t, foreign.Options{ControlPlanes: 3, Workers: 1, UserManifest: true})
	before := cniPaths(t, c)
	out, err := adoptArgs(t, c, append(recoverFlags(c), "--cni-unmanaged", flagPlan, "-o", "yaml")...)
	if err != nil {
		t.Fatalf("adopt %s: %v", flagPlan, err)
	}
	if n := strings.Count(out, "patch ready"); n != 4 {
		t.Errorf("adopt %s: %d machines have a patch ready, want 4", flagPlan, n)
	}
	if !strings.Contains(out, "userapp") {
		t.Errorf("adopt %s output does not carry the user inline manifest %q", flagPlan, "userapp")
	}
	sameHashes(t, before, cniPaths(t, c))
	nothingWritten(t, c)
}
