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

package v1alpha1

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKeyRotationTokenSettleRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		settle  *metav1.Duration
		refused bool
	}{
		"unset takes the default": {nil, false},
		"the default":             {&metav1.Duration{Duration: DefaultKeyRotationTokenSettle}, false},
		"exactly the minimum":     {&metav1.Duration{Duration: MinKeyRotationTokenSettle}, false},
		"a day":                   {&metav1.Duration{Duration: 24 * time.Hour}, false},
		"just under the minimum":  {&metav1.Duration{Duration: MinKeyRotationTokenSettle - time.Second}, true},
		"five minutes":            {&metav1.Duration{Duration: 5 * time.Minute}, true},
		"zero":                    {&metav1.Duration{}, true},
	} {
		spec := &TalosOperationKeyRotationSpec{Targets: []string{KeyRotationTargetServiceAccount}, TokenSettle: tc.settle}

		if got := spec.TokenSettleRefusal(); (got != "") != tc.refused {
			t.Errorf("%s: refusal %q, want refused=%v", name, got, tc.refused)
		}
	}

	// The message says why and what to do instead.
	short := &TalosOperationKeyRotationSpec{
		Targets: []string{KeyRotationTargetServiceAccount}, TokenSettle: &metav1.Duration{Duration: 5 * time.Minute},
	}

	msg := short.TokenSettleRefusal()
	for _, want := range []string{"5m0s", MinKeyRotationTokenSettle.String(), "projected", "kubelet"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal %q does not mention %q", msg, want)
		}
	}

	// A rotation of the secretbox key alone has no tokens to wait for.
	box := &TalosOperationKeyRotationSpec{Targets: []string{KeyRotationTargetSecretbox}, TokenSettle: &metav1.Duration{}}
	if got := box.TokenSettleRefusal(); got != "" {
		t.Errorf("a secretbox-only rotation ignores tokenSettle, got refusal %q", got)
	}
}

func TestKeyRotationOutdatesSnapshot(t *testing.T) {
	snap := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	after, before := metav1.NewTime(snap.Add(time.Hour)), metav1.NewTime(snap.Add(-time.Hour))

	keyOp := func(phase, stage string, finished *metav1.Time) *TalosOperation {
		op := &TalosOperation{Spec: TalosOperationSpec{Kind: TalosOperationKindKeyRotation}}
		op.Status.Phase, op.Status.FinishedAt = phase, finished

		if stage != "" {
			op.Status.KeyRotation = &TalosOperationKeyRotationStatus{Stage: stage}
		}

		return op
	}

	for name, tc := range map[string]struct {
		op   *TalosOperation
		want bool
	}{
		"finished after the snapshot":     {keyOp(TalosOperationPhaseCompleted, KeyRotationStageDone, &after), true},
		"finished before the snapshot":    {keyOp(TalosOperationPhaseCompleted, KeyRotationStageDone, &before), false},
		"under way":                       {keyOp(TalosOperationPhaseInProgress, KeyRotationStageIssuing, nil), true},
		"paused in a stage":               {keyOp(TalosOperationPhasePaused, KeyRotationStageAccepting, nil), true},
		"never started":                   {keyOp(TalosOperationPhasePending, "", nil), false},
		"completed without a finish time": {keyOp(TalosOperationPhaseCompleted, KeyRotationStageDone, nil), false},
		"another kind":                    {&TalosOperation{Spec: TalosOperationSpec{Kind: TalosOperationKindCARotation}}, false},
	} {
		if got := KeyRotationOutdatesSnapshot(tc.op, snap); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
