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

// Package adr guards README.md's own index against drifting out of sync
// with the ADRs actually on disk -- the same "generated/listed state must
// match reality" idea dist/chart/chart_test.go already applies to the
// chart, applied here to a hand-maintained table instead of a generated
// file.
package adr

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

var filenamePattern = regexp.MustCompile(`^(\d{4})-.*\.md$`)

// adrNumbers lists every real ADR number on disk: a 4-digit-prefixed .md
// file, excluding 0000-template.md (the template, not a decision) and
// README.md itself.
func adrNumbers(t *testing.T) []int {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading docs/adr: %v", err)
	}

	var numbers []int
	for _, e := range entries {
		if e.IsDir() || e.Name() == "0000-template.md" {
			continue
		}
		m := filenamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("file %q has a non-numeric ADR prefix: %v", e.Name(), err)
		}
		numbers = append(numbers, n)
	}
	return numbers
}

var tableRowPattern = regexp.MustCompile(`^\|\s*(\d{4})\s*\|`)

// indexedNumbers lists every ADR number README.md's table actually has a
// row for.
func indexedNumbers(t *testing.T) []int {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(".", "README.md"))
	if err != nil {
		t.Fatalf("reading docs/adr/README.md: %v", err)
	}

	var numbers []int
	for _, line := range splitLines(string(data)) {
		m := tableRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("README.md table row %q has a non-numeric ADR number: %v", line, err)
		}
		numbers = append(numbers, n)
	}
	return numbers
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, c := range s {
		if c == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// TestIndexMatchesFiles fails loudly, naming the exact number, whenever
// README.md's table and the ADRs actually on disk disagree: a file with no
// row (the index went stale, exactly what happened twice before this test
// existed) or a row with no file (a renumber or deletion the index was
// never updated for).
func TestIndexMatchesFiles(t *testing.T) {
	onDisk := toSet(adrNumbers(t))
	indexed := toSet(indexedNumbers(t))

	for n := range onDisk {
		if !indexed[n] {
			t.Errorf("ADR %04d exists on disk but has no row in README.md's index; add one", n)
		}
	}
	for n := range indexed {
		if !onDisk[n] {
			t.Errorf("README.md's index lists ADR %04d, but no such file exists in docs/adr/", n)
		}
	}
}

func toSet(nums []int) map[int]bool {
	set := make(map[int]bool, len(nums))
	for _, n := range nums {
		set[n] = true
	}
	return set
}
