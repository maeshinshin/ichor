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

// TestF4VersionSkew (ADR 0104 F4): run against a cluster where the packaged chart is installed with Helm (after F3 or
// F1, ICHOR_E2E_F_KEEP=1). The alternate CLI (ICHOR_E2E_F_ICHOR_ALT_BIN) has another version than the chart:
// adopt refuses and prints the helm upgrade command, and writes nothing.
func TestF4VersionSkew(t *testing.T) {
	c := build(t, foreign.Options{Workers: 1})
	stageAlt(t, c)
	before := releaseState(c)
	out, err := adoptBin(t, c, "/tmp/ichor-alt", "--secrets-bundle", "/tmp/home/.ichor/clusters/"+c.Opts.Prefix+"/secrets.yaml")
	if err == nil {
		t.Fatalf("adopt with a skewed CLI succeeded:\n%s", out)
	}
	if !strings.Contains(out, "helm upgrade") || !strings.Contains(out, "--version 0.0.5025-gbad") {
		t.Errorf("the refusal does not carry the helm upgrade command for the CLI's version")
	}
	if after := releaseState(c); after != before {
		t.Errorf("Helm release records changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
