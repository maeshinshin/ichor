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

package cli

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sequenceBootstrap returns errs in order, then nil.
type sequenceBootstrap struct {
	errs  []error
	calls int
}

func (s *sequenceBootstrap) Bootstrap(context.Context) error {
	s.calls++
	if s.calls > len(s.errs) {
		return nil
	}

	return s.errs[s.calls-1]
}

func (*sequenceBootstrap) Kubeconfig(context.Context) ([]byte, error) { return nil, nil }

func TestBootstrapWhenUp_RetriesUnavailable(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "handshake failed")
	b := &sequenceBootstrap{errs: []error{unavailable, status.Error(codes.FailedPrecondition, "bootstrap is not available yet")}}

	if err := bootstrapWhenUp(context.Background(), b, time.Minute, time.Millisecond, io.Discard); err != nil {
		t.Fatalf("bootstrapWhenUp: %v", err)
	}

	if b.calls != 3 {
		t.Fatalf("Bootstrap called %d times, want 3", b.calls)
	}
}

func TestBootstrapWhenUp_OtherErrorsReturnAtOnce(t *testing.T) {
	b := &sequenceBootstrap{errs: []error{errors.New("etcd already bootstrapped")}}

	if err := bootstrapWhenUp(context.Background(), b, time.Minute, time.Millisecond, io.Discard); err == nil {
		t.Fatal("expected the error")
	}

	if b.calls != 1 {
		t.Fatalf("Bootstrap called %d times, want 1", b.calls)
	}
}

func TestBootstrapWhenUp_GivesUpAfterTheWait(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "still down")
	b := &sequenceBootstrap{errs: make([]error, 1000)}

	for i := range b.errs {
		b.errs[i] = unavailable
	}

	err := bootstrapWhenUp(context.Background(), b, 20*time.Millisecond, 5*time.Millisecond, io.Discard)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want the last Unavailable", err)
	}
}
