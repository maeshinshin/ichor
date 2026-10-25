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
	"strings"
	"testing"
)

// TestBuildPRBody_RequesterAndDiffAlways proves the two things
// architecture.md names ("who asked" and the change itself) are always
// present, and that an empty impact preview adds nothing (no stray
// "## Impact" heading with nothing under it).
func TestBuildPRBody_RequesterAndDiffAlways(t *testing.T) {
	body := buildPRBody("ichor-ui", "-old\n+new\n", "")

	if !strings.Contains(body, "Requested by: ichor-ui") {
		t.Fatalf("missing requester, got:\n%s", body)
	}

	if !strings.Contains(body, "-old\n+new\n") {
		t.Fatalf("missing diff, got:\n%s", body)
	}

	if strings.Contains(body, "Impact") {
		t.Fatalf("expected no Impact section for an empty impactPreview, got:\n%s", body)
	}
}

// TestBuildPRBody_ImpactPreviewSlot proves a non-empty impact preview is
// included verbatim -- this package never computes one itself (that
// backend does not exist yet), only reserves the slot.
func TestBuildPRBody_ImpactPreviewSlot(t *testing.T) {
	body := buildPRBody("ichor-ui", "-old\n+new\n", "3 nodes affected; no reboot required")

	if !strings.Contains(body, "3 nodes affected; no reboot required") {
		t.Fatalf("missing impact preview, got:\n%s", body)
	}
}
