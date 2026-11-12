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
	"encoding/json"
	"time"
)

// checkpoint is one machine's high-water mark within a resume token: the
// last-seen log timestamp, plus a monotonic ordinal that breaks ties
// between lines this stream observed at the same timestamp. The ordinal only orders lines within one
// StreamNodeLogs call; a resume against a different backend process (after
// a drain) has no stable per-line identity to recover across the gap --
// Talos's Dmesg API offers none -- so a resumed stream dedupes on timestamp
// alone and accepts a small window of possible re-delivery or gap right at
// the reconnect point. That limit is why logs stay out of
// the "exactly resumed" category the design reserves for resourceVersion-
// based watches.
type checkpoint struct {
	Time    time.Time `json:"t"`
	Ordinal uint64    `json:"o"`
}

// resumeToken is opaque on the wire (StreamNodeLogsRequest/Response's own
// doc comments: "nothing requires the client to parse it"); JSON plus
// base64 is simply the encoding this backend picked, not a contract any
// client is allowed to depend on.
type resumeToken map[string]checkpoint

// decodeResumeToken parses s, the client's last-received resume_token.
// An empty string (the "start from the current tail" case) and a token
// this backend cannot parse are treated the same way -- start fresh with no
// checkpoints -- since the alternative (refusing the whole call over a
// token whose shape might simply have changed across a version) is worse
// than replaying a little more log than strictly necessary.
func decodeResumeToken(s string) resumeToken {
	if s == "" {
		return resumeToken{}
	}

	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return resumeToken{}
	}

	var tok resumeToken
	// JSON null decodes to a nil map without an error, and advance writes to it.
	if err := json.Unmarshal(raw, &tok); err != nil || tok == nil {
		return resumeToken{}
	}

	return tok
}

// encode renders tok as the opaque string StreamNodeLogsResponse.resume_token
// carries.
func (tok resumeToken) encode() (string, error) {
	raw, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// advance records that line was just sent for machine key, bumping the
// ordinal when line.Time repeats the previous checkpoint's timestamp
// exactly, and returns the updated token.
func (tok resumeToken) advance(key string, line LogLine) resumeToken {
	next := checkpoint{Time: line.Time, Ordinal: 0}
	if prev, ok := tok[key]; ok && prev.Time.Equal(line.Time) {
		next.Ordinal = prev.Ordinal + 1
	}

	tok[key] = next

	return tok
}
