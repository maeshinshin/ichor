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
	"errors"
	"fmt"
	"testing"
)

func TestPauseReasonReadsTheCiliumJobsTypedErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"invalid values":  {&CNIInvalidValuesError{Err: errors.New("bad")}, reasonCNIInvalidValues},
		"job failed":      {&CNIHelmJobFailedError{JobName: "j", Reason: "BackoffLimitExceeded"}, reasonCNIJobFailed},
		"job without pod": {&CNIHelmJobFailedError{JobName: "j", Reason: "PodNotCreated", NoPod: true}, reasonCNIJobFailed},
		"wrapped, as runCiliumJob returns it": {
			fmt.Errorf("upgrading Cilium: %w", &CNIInvalidValuesError{Err: errors.New("bad")}), reasonCNIInvalidValues,
		},
		"anything else": {errors.New("boom"), reasonOperationStepFailed},
	} {
		if got := pauseReason(tc.err); got != tc.want {
			t.Errorf("%s: pauseReason = %q, want %q", name, got, tc.want)
		}

		if !isStepFailure(tc.want) {
			t.Errorf("%s: %q must be a step failure, or the pause keeps the lock and sets no failureReason", name, tc.want)
		}
	}
}
