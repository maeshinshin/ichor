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
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// flakyOps fails GetOperation with errs in order, then reports Completed.
type flakyOps struct {
	ichorv1alpha1connect.OperationServiceClient
	errs  []error
	calls int
}

func (f *flakyOps) GetOperation(context.Context, *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.GetOperationResponse], error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return nil, f.errs[f.calls-1]
	}

	return connect.NewResponse(&pb.GetOperationResponse{Operation: &pb.Operation{Phase: "Completed"}}), nil
}

func shrinkFollowTimers(t *testing.T) {
	t.Helper()

	oi, od, ow := opWatchInterval, followRetryDelay, followRetryWindow
	opWatchInterval, followRetryDelay, followRetryWindow = time.Millisecond, time.Millisecond, time.Second

	t.Cleanup(func() { opWatchInterval, followRetryDelay, followRetryWindow = oi, od, ow })
}

func TestWatchRetriesTransientErrors(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &flakyOps{errs: []error{
		errors.New("Get \"https://10.0.0.1:6443/apis\": dial tcp 10.0.0.1:6443: connect: connection refused"),
		connect.NewError(connect.CodeUnavailable, errors.New("tunnel gone")),
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("want success after retries, got %v", err)
	}

	if ops.calls != 3 || strings.Count(errw.String(), "API unreachable, retrying") != 2 {
		t.Fatalf("calls=%d stderr=%q", ops.calls, errw.String())
	}
}

func TestWatchStopsAtPermanentErrors(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &flakyOps{errs: []error{connect.NewError(connect.CodeNotFound, errors.New("gone"))}}
	c := &apiClients{Operation: ops, Close: func() {}}

	err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &bytes.Buffer{})
	if connect.CodeOf(err) != connect.CodeNotFound || ops.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, ops.calls)
	}
}

func TestWatchGivesUpAfterTheRetryWindow(t *testing.T) {
	shrinkFollowTimers(t)

	followRetryWindow = 30 * time.Millisecond
	refused := errors.New("dial tcp: connect: connection refused")
	ops := &flakyOps{errs: make([]error, 100000)}

	for i := range ops.errs {
		ops.errs[i] = refused
	}

	c := &apiClients{Operation: ops, Close: func() {}}

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &bytes.Buffer{}); !errors.Is(err, refused) {
		t.Fatalf("want the last error, got %v", err)
	}
}

func TestWatchRestoreWaitsOutNotFound(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &flakyOps{errs: []error{
		connect.NewError(connect.CodeNotFound, errors.New("operation x not found")),
		connect.NewError(connect.CodeNotFound, errors.New("operation x not found")),
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var errw bytes.Buffer

	if err := watchRestoreOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &errw); err != nil {
		t.Fatalf("want success once the operation is back, got %v", err)
	}

	if ops.calls != 3 || strings.Count(errw.String(), "waiting for the restore to resume") != 1 {
		t.Fatalf("calls=%d stderr=%q", ops.calls, errw.String())
	}
}

func TestWatchRestoreGivesUpOnNotFoundAfterTheWindow(t *testing.T) {
	shrinkFollowTimers(t)

	old := restoreResumeWindow
	restoreResumeWindow = 30 * time.Millisecond

	t.Cleanup(func() { restoreResumeWindow = old })

	ops := &flakyOps{errs: make([]error, 100000)}
	for i := range ops.errs {
		ops.errs[i] = connect.NewError(connect.CodeNotFound, errors.New("gone"))
	}

	c := &apiClients{Operation: ops, Close: func() {}}

	err := watchRestoreOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &bytes.Buffer{})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("want the NotFound once the window is spent, got %v", err)
	}
}

func TestWatchRedialsADeadTunnel(t *testing.T) {
	shrinkFollowTimers(t)

	dead := &flakyOps{errs: []error{connect.NewError(connect.CodeUnavailable, errors.New("eof"))}}
	fresh := &flakyOps{}
	c := &apiClients{Operation: dead, Close: func() {}, Reconnect: func(context.Context) (*apiClients, error) {
		return &apiClients{Operation: fresh, Close: func() {}}, nil
	}}

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	if fresh.calls != 1 {
		t.Fatalf("fresh calls=%d", fresh.calls)
	}
}
