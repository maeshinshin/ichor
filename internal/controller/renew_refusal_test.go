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
	"fmt"
	"testing"

	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

// Lock contention is expected while another reconcile of this controller holds the lock: it must
// requeue, not come back as a reconcile error (which logs a stack and backs off).
func TestRenewRefusalRequeuesOnContention(t *testing.T) {
	for _, holder := range []string{controllerIdentity, ""} {
		err := fmt.Errorf("renewing: %w", &clusterlock.HeldError{Holder: holder})

		res, got := renewRefusal(context.Background(), err)
		if got != nil {
			t.Fatalf("holder %q: contention returned error %v, want nil", holder, got)
		}
		if res.RequeueAfter != operationRequeueInterval {
			t.Fatalf("holder %q: RequeueAfter = %v, want %v", holder, res.RequeueAfter, operationRequeueInterval)
		}
	}
}

func TestRenewRefusalReturnsOtherErrors(t *testing.T) {
	boom := errors.New("api down")

	res, got := renewRefusal(context.Background(), boom)
	if !errors.Is(got, boom) {
		t.Fatalf("error = %v, want %v", got, boom)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("RequeueAfter = %v, want 0", res.RequeueAfter)
	}
}
