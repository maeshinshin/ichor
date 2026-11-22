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

package transient_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maeshinshin/ichor/internal/transient"
)

// A Talos node that does not answer (its API blocked, or the node stopped) fails with a gRPC status error, which
// does not unwrap to context.DeadlineExceeded. Hardware run, 2026-11-22: such a failure paused an upgrade for a
// person instead of being retried (ADR 0174).
func TestIsGRPCStatus(t *testing.T) {
	t.Parallel()

	const waiting = "context deadline exceeded while waiting for connections to become ready"

	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"deadline exceeded": {status.Error(codes.DeadlineExceeded, waiting), true},
		"unavailable":       {status.Error(codes.Unavailable, "connection error"), true},
		"wrapped twice":     {fmt.Errorf("cannot tell: %w: %w", errors.New("unreadable"), status.Error(codes.DeadlineExceeded, waiting)), true},
		"permission denied": {status.Error(codes.PermissionDenied, "no"), false},
		"not found":         {status.Error(codes.NotFound, "no"), false},
		"plain":             {errors.New("a refusal"), false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := transient.Is(tc.err); got != tc.want {
				t.Fatalf("Is(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
