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
	"strings"
	"testing"
)

// TestSuspiciousSurvivorsAreReviewed fails when a field whose name looks
// credential-shaped (suspiciousKeywords) is in the mirror without being
// dropped by secretFields or listed, with a reason, in reviewedSurvivors.
// Before this test the scan only printed a report, and a registry client
// private key and the extraManifests headers shipped while listed in it.
func TestSuspiciousSurvivorsAreReviewed(t *testing.T) {
	g := generate()

	if unreviewed := g.suspiciousSurvivors(); len(unreviewed) > 0 {
		lines := make([]string, 0, len(unreviewed))
		for _, s := range unreviewed {
			lines = append(lines, "  "+s.key+" (yaml \""+s.yaml+"\")")
		}

		t.Errorf("fields whose name suggests a credential are in the mirror and neither dropped nor reviewed:\n%s\n"+
			"Credential: add it to secretFields (main.go). Not a credential: add it to reviewedSurvivors "+
			"(suspicious.go) with the reason.", strings.Join(lines, "\n"))
	}

	live := map[string]bool{}
	for _, s := range g.suspiciousFields() {
		live[s.key] = true
	}

	for key, why := range reviewedSurvivors {
		if strings.TrimSpace(why) == "" {
			t.Errorf("reviewedSurvivors[%q]: the reason is empty", key)
		}

		if !live[key] {
			t.Errorf("reviewedSurvivors[%q] matches no surviving field (renamed, dropped or no longer suspicious); delete it", key)
		}
	}
}
