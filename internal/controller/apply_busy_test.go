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
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Hardware run, 2026-11-27: a dry run racing another apply on one node paused the "API server authentication"
// rollout. The busy refusal is retried inside the call; any other error is returned after one call.
func TestRetryApplyBusy(t *testing.T) {
	t.Parallel()

	busy := status.Error(codes.FailedPrecondition, "another apply configuration is already in progress")

	for name, tc := range map[string]struct {
		errs      []error
		wantErr   bool
		wantCalls int
	}{
		"busy once, then success": {[]error{busy, nil}, false, 2},
		"permanent error":         {[]error{status.Error(codes.PermissionDenied, "no")}, true, 1},
		"busy for ever":           {[]error{busy}, true, applyBusyRetries + 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			err := retryApplyBusy(context.Background(), time.Microsecond, func() error {
				e := tc.errs[min(calls, len(tc.errs)-1)]
				calls++

				return e
			})

			if (err != nil) != tc.wantErr || calls != tc.wantCalls {
				t.Fatalf("err=%v calls=%d, want error=%v calls=%d", err, calls, tc.wantErr, tc.wantCalls)
			}
		})
	}

	if err := retryApplyBusy(context.Background(), time.Microsecond, func() error { return errors.New("x") }); err == nil {
		t.Fatal("a plain error must be returned")
	}
}
