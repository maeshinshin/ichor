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
	"strings"
	"testing"
)

// TestScrubberLongestFirst: when one secret is a substring of another, registering the short one
// first must not leave the tail of the long one behind.
func TestScrubberLongestFirst(t *testing.T) {
	const short, long = "abcd", "abcd-efgh-ijkl"

	for _, order := range [][]string{{short, long}, {long, short}} {
		var s scrubber

		s.add(order...)

		got := s.clean("key=" + long + " and " + short)
		if strings.Contains(got, "efgh") || strings.Contains(got, "ijkl") || strings.Contains(got, short) {
			t.Fatalf("order %v: secret left in %q", order, got)
		}
	}
}
