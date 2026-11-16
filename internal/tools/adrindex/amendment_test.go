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

// headingWithoutMarker is the forward rule: the body records an amendment
// but the status does not mark the ADR itself as amended.
func headingWithoutMarker(status string, body []byte) bool {
	return amendmentHeading.Match(body) && !hasSelfMarker(status)
}

// markerWithoutHeading is the converse rule: the status marks the ADR itself
// as amended but the body has no amendment heading to show for it. The
// heading is the same one the forward rule recognises, so the two rules agree
// on what counts as an amendment.
func markerWithoutHeading(status string, body []byte) bool {
	return hasSelfMarker(status) && !amendmentHeading.Match(body)
}

// adrText is one parsed ADR with its body, so both directions read every
// file once and take the status from the generator's own statusLine regex.
type adrText struct {
	entry
	body []byte
}

func loadADRs(t *testing.T) []adrText {
	t.Helper()

	entries, err := parseADRs(adrDir)
	if err != nil {
		t.Fatalf("parseADRs: %v", err)
	}

	out := make([]adrText, 0, len(entries))

	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(adrDir, e.File))
		if err != nil {
			t.Fatalf("reading %s: %v", e.File, err)
		}

		out = append(out, adrText{entry: e, body: body})
	}

	return out
}

// TestAmendedADRsCarryTheIndexMarker fails when an ADR has an amendment
// heading in its body but its Status: value does not carry "amended". Such an
// ADR is missing from the generated index's Status column.
func TestAmendedADRsCarryTheIndexMarker(t *testing.T) {
	var missing []string

	for _, a := range loadADRs(t) {
		if headingWithoutMarker(a.Status, a.body) {
			missing = append(missing, fmt.Sprintf("%s: Status %q", a.File, a.Status))
		}
	}

	if len(missing) > 0 {
		t.Errorf("%d ADR(s) have an amendment heading but no `amended` in the Status value before `· Date:`:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestAmendmentMarkerHasAnAmendmentHeading is the converse of
// TestAmendedADRsCarryTheIndexMarker: an ADR whose Status: value marks itself
// as amended must have an amendment heading in its body. It fails when an
// amendment was folded into a decision and the marker outlived it (ADR 0020).
func TestAmendmentMarkerHasAnAmendmentHeading(t *testing.T) {
	var orphaned []string

	for _, a := range loadADRs(t) {
		if markerWithoutHeading(a.Status, a.body) {
			orphaned = append(orphaned, fmt.Sprintf("%s: Status %q", a.File, a.Status))
		}
	}

	if len(orphaned) > 0 {
		t.Errorf("%d ADR(s) say `amended` in the Status value but have no amendment heading (`## Amendment`, `## Amendments`, `## Addendum`) to show for it:\n  %s",
			len(orphaned), strings.Join(orphaned, "\n  "))
	}
}

const (
	bodyWithAmendment    = "## Context\n\n## Amendment: a later decision\n"
	bodyWithoutAmendment = "## Context\n\n## Decision\n"
)

// TestAmendmentDirectionsOnStatusForms pins both rules on the status forms
// the ADRs use, against synthetic bodies, so the converse's "amended by"
// handling is checked without depending on which ADRs exist today.
func TestAmendmentDirectionsOnStatusForms(t *testing.T) {
	cases := []struct {
		name         string
		status       string
		body         string
		wantForward  bool // headingWithoutMarker
		wantConverse bool // markerWithoutHeading
	}{
		{
			name:   "plain self-marker with its heading",
			status: "Accepted, amended",
			body:   bodyWithAmendment,
		},
		{
			name:         "plain self-marker with no heading",
			status:       "Accepted, amended",
			body:         bodyWithoutAmendment,
			wantConverse: true,
		},
		{
			name:         "self-marker beside a cross-reference, no heading (ADR 0063 form)",
			status:       "Accepted, amended (the stage sequence is amended by ADR 0091)",
			body:         bodyWithoutAmendment,
			wantConverse: true,
		},
		{
			name:   "self-marker beside a cross-reference, with its heading",
			status: "Accepted, amended (the stage sequence is amended by ADR 0091)",
			body:   bodyWithAmendment,
		},
		{
			name:         "parenthesised marker after a superseded clause, no heading",
			status:       "Accepted (amended); superseded in part by ADR 0099",
			body:         bodyWithoutAmendment,
			wantConverse: true,
		},
		{
			name:   "cross-reference only, no heading (ADR 0065 form)",
			status: "Accepted, amended by ADR 0110",
			body:   bodyWithoutAmendment,
		},
		{
			name:        "cross-reference only but a heading present",
			status:      "Accepted, amended by ADR 0110",
			body:        bodyWithAmendment,
			wantForward: true,
		},
		{
			name:         "cross-reference then a real self-marker, no heading",
			status:       "Accepted, amended by ADR 0110; amended once",
			body:         bodyWithoutAmendment,
			wantConverse: true,
		},
		{
			name:   "free-form status with no marker, no heading",
			status: "Accepted (implemented 2026-10-27)",
			body:   bodyWithoutAmendment,
		},
		{
			name:        "free-form status with no marker but a heading",
			status:      "Accepted (UI and webhook added 2026-10-26)",
			body:        bodyWithAmendment,
			wantForward: true,
		},
		{
			name:   "amendment as a noun is not a marker",
			status: "Accepted, amendment pending review",
			body:   bodyWithoutAmendment,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := headingWithoutMarker(tc.status, []byte(tc.body)); got != tc.wantForward {
				t.Errorf("headingWithoutMarker(%q) = %v, want %v", tc.status, got, tc.wantForward)
			}

			if got := markerWithoutHeading(tc.status, []byte(tc.body)); got != tc.wantConverse {
				t.Errorf("markerWithoutHeading(%q) = %v, want %v", tc.status, got, tc.wantConverse)
			}
		})
	}
}
