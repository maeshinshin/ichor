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

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// amendmentHeading is a body heading that records an amendment, in the
// forms the ADRs use: "## Amendment ...", "## Amendments ...", "### Amendment ..."
// (ADR 0120), and "## Addendum ..." (ADRs 0009, 0054, 0055, 0063).
var amendmentHeading = regexp.MustCompile(`(?m)^#{2,3} +(?:Second +)?(?:Amendments?|Addenda|Addendum)\b`)

// selfMarker finds "amended" in a status value. "amended by ..." is
// excluded by the group, because it says another ADR amended this one.
var selfMarker = regexp.MustCompile(`\bamended\b(\s+by\b)?`)

// hasSelfMarker reports whether status (the text statusLine captures,
// before "· Date:") marks this ADR itself as amended.
func hasSelfMarker(status string) bool {
	for _, m := range selfMarker.FindAllStringSubmatch(status, -1) {
		if m[1] == "" {
			return true
		}
	}

	return false
}

// TestAmendedADRsCarryTheIndexMarker fails when an ADR has an amendment
// heading in its body but its Status: value, as parseADRs reads it through
// the generator's own statusLine, does not carry "amended". Such an ADR is
// missing from the generated index's Status column. Uses parseADRs so the
// status text comes from the same regex the generator uses.
func TestAmendedADRsCarryTheIndexMarker(t *testing.T) {
	entries, err := parseADRs(adrDir)
	if err != nil {
		t.Fatalf("parseADRs: %v", err)
	}

	var missing []string

	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(adrDir, e.File))
		if err != nil {
			t.Fatalf("reading %s: %v", e.File, err)
		}

		if !amendmentHeading.Match(body) || hasSelfMarker(e.Status) {
			continue
		}

		missing = append(missing, fmt.Sprintf("%s: Status %q", e.File, e.Status))
	}

	if len(missing) > 0 {
		t.Errorf("%d ADR(s) have an amendment heading but no `amended` in the Status value before `· Date:`:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}
