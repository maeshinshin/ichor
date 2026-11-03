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

package storage

import (
	"testing"
	"time"
)

func TestNewEvacuationHook(t *testing.T) {
	h, err := NewEvacuationHook(LonghornV1)
	if err != nil {
		t.Fatalf("NewEvacuationHook(%q) error: %v", LonghornV1, err)
	}

	if _, ok := h.(longhornV1); !ok {
		t.Fatalf("NewEvacuationHook(%q) = %T, want longhornV1", LonghornV1, h)
	}

	if _, err := NewEvacuationHook("rook-v1"); err == nil {
		t.Fatal(`NewEvacuationHook("rook-v1") = nil error, want an error (not an allowed preset)`)
	}
}

func TestLonghornV1_BeginAndEndEvacuation(t *testing.T) {
	h := longhornV1{}

	// BeginEvacuation must disable scheduling and request eviction:
	// Longhorn's admission webhook rejects evictionRequested=true while
	// allowScheduling is still true, so allowScheduling must be the one
	// that goes false here, not true.
	if allow, evict := h.BeginEvacuation(); allow || !evict {
		t.Errorf("BeginEvacuation() = (%v, %v), want (false, true)", allow, evict)
	}

	// EndEvacuation must be the exact inverse.
	if allow, evict := h.EndEvacuation(); !allow || evict {
		t.Errorf("EndEvacuation() = (%v, %v), want (true, false)", allow, evict)
	}
}

func TestEvacuationStatus(t *testing.T) {
	const timeout = 5 * time.Minute

	cases := []struct {
		name              string
		replicasRemaining int
		elapsed           time.Duration
		want              EvacuationProgress
	}{
		{name: "replicas gone, well within timeout", replicasRemaining: 0, elapsed: time.Minute, want: EvacuationDone},
		{name: "replicas gone exactly at timeout", replicasRemaining: 0, elapsed: timeout, want: EvacuationDone},
		{name: "replicas remain, within timeout", replicasRemaining: 2, elapsed: time.Minute, want: EvacuationWaiting},
		{name: "replicas remain, exactly at timeout", replicasRemaining: 1, elapsed: timeout, want: EvacuationPaused},
		{name: "replicas remain, past timeout", replicasRemaining: 1, elapsed: timeout + time.Second, want: EvacuationPaused},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvacuationStatus(tc.replicasRemaining, tc.elapsed, timeout); got != tc.want {
				t.Errorf("EvacuationStatus(%d, %v, %v) = %v, want %v", tc.replicasRemaining, tc.elapsed, timeout, got, tc.want)
			}
		})
	}
}
