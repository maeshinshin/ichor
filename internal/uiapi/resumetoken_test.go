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

package uiapi

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestResumeTokenRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tok := resumeToken{}.advance("default/m1", LogLine{Time: now, Message: "a"})
	tok = tok.advance("default/m2", LogLine{Time: now.Add(time.Second), Message: "b"})

	s, err := tok.encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	got := decodeResumeToken(s)

	if !got["default/m1"].Time.Equal(now) {
		t.Fatalf("m1 time = %v, want %v", got["default/m1"].Time, now)
	}

	if !got["default/m2"].Time.Equal(now.Add(time.Second)) {
		t.Fatalf("m2 time = %v, want %v", got["default/m2"].Time, now.Add(time.Second))
	}
}

func TestResumeTokenOrdinalBreaksTies(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tok := resumeToken{}
	tok = tok.advance("default/m1", LogLine{Time: now, Message: "a"})

	if tok["default/m1"].Ordinal != 0 {
		t.Fatalf("first line ordinal = %d, want 0", tok["default/m1"].Ordinal)
	}

	tok = tok.advance("default/m1", LogLine{Time: now, Message: "b"})

	if tok["default/m1"].Ordinal != 1 {
		t.Fatalf("second same-timestamp line ordinal = %d, want 1", tok["default/m1"].Ordinal)
	}

	tok = tok.advance("default/m1", LogLine{Time: now.Add(time.Millisecond), Message: "c"})

	if tok["default/m1"].Ordinal != 0 {
		t.Fatalf("ordinal did not reset on a new timestamp: got %d", tok["default/m1"].Ordinal)
	}
}

func TestDecodeResumeTokenTreatsEmptyAndGarbageAsFresh(t *testing.T) {
	if got := decodeResumeToken(""); len(got) != 0 {
		t.Fatalf("empty token: got %v, want empty", got)
	}

	if got := decodeResumeToken("not-a-valid-token!!"); len(got) != 0 {
		t.Fatalf("garbage token: got %v, want empty, not an error", got)
	}
}

// Every shape a client can send must come back usable: advance writes to the map.
func TestDecodeResumeTokenNeverReturnsAnUnusableToken(t *testing.T) {
	for name, raw := range map[string]string{
		"null": "null", "array": "[]", "number": "1", "string": `"x"`, "empty object": "{}",
		"null checkpoint": `{"default/m1":null}`, "wrong checkpoint type": `{"default/m1":1}`,
	} {
		tok := decodeResumeToken(base64.RawURLEncoding.EncodeToString([]byte(raw)))
		if tok == nil {
			t.Fatalf("%s: got a nil token", name)
		}

		tok = tok.advance("default/m1", LogLine{Time: time.Unix(1, 0), Message: "a"})
		if tok["default/m1"].Time.IsZero() {
			t.Fatalf("%s: advance did not record the line", name)
		}
	}
}
