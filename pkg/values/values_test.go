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

package values

import (
	"reflect"
	"strings"
	"testing"
)

func TestEffective(t *testing.T) {
	defaults := map[string]any{"a": map[string]any{"x": 1, "y": 2}, keyList: []any{1, 2}, "d": "def"}
	user := map[string]any{"a": map[string]any{"y": 3, "z": 4}, keyList: []any{9}, "o": "user"}
	owned := map[string]any{"o": "owned", "a": map[string]any{"x": 100}}

	got := Effective(defaults, user, owned)
	want := map[string]any{
		"a":     map[string]any{"x": 100.0, "y": 3.0, "z": 4.0},
		keyList: []any{9.0},
		"d":     "def",
		"o":     "owned",
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Effective = %v, want %v", got, want)
	}

	if defaults["a"].(map[string]any)["y"] != 2 {
		t.Error("Effective mutated its input")
	}
}

func TestCheckSize(t *testing.T) {
	if err := CheckSize(map[string]any{"a": "b"}); err != nil {
		t.Errorf("small values: %v", err)
	}

	err := CheckSize(map[string]any{"a": strings.Repeat("x", MaxSize)})
	if err == nil {
		t.Error("oversized values accepted")
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", strABC, 3}, {strABC, strABC, 0}, {"masqurade", "masquerade", 1}, {"kitten", "sitting", 3}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

const (
	keyList = "list"
	strABC  = "abc"
)
