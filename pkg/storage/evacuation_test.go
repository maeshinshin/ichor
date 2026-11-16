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
	"strings"
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

}

// rook-v1 is an allowed preset with no evacuation hook: resolving one must return an error, not panic on the
// type assertion.
func TestNewEvacuationHook_RookV1IsAnErrorNotAPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf(`NewEvacuationHook("rook-v1") panicked: %v`, r)
		}
	}()

	if _, err := NewEvacuationHook("rook-v1"); err == nil {
		t.Fatal(`NewEvacuationHook("rook-v1") = nil error, want an error (rook-v1 has no evacuation hook)`)
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

// notAHook is a Preset without BeginEvacuation or EndEvacuation: what a second preset is if it is written
// without the hook. Its Preset methods are never called.
type notAHook struct{ Preset }

func TestEvacuationHookOf_PresetWithoutHookIsAnError(t *testing.T) {
	h, err := evacuationHookOf("stub-v1", notAHook{})
	if err == nil {
		t.Fatalf("evacuationHookOf(preset without hook) = %T, nil error; want an error", h)
	}

	if !strings.Contains(err.Error(), `"stub-v1"`) {
		t.Fatalf("evacuationHookOf error %q does not name the preset", err)
	}
}
