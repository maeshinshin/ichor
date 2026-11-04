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
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"k8s.io/client-go/rest"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

const (
	opPhaseDone = "Completed"
	cp1URL      = "https://10.0.0.1:6443"
	cp2URL      = "https://10.0.0.2:6443"
	cp3URL      = "https://10.0.0.3:6443"
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

	return connect.NewResponse(&pb.GetOperationResponse{Operation: &pb.Operation{Phase: opPhaseDone}}), nil
}

func shrinkFollowTimers(t *testing.T) {
	t.Helper()

	oi, od, ow := opWatchInterval, followRetryDelay, followRetryWindow
	opWatchInterval, followRetryDelay, followRetryWindow = time.Millisecond, time.Millisecond, time.Second

	t.Cleanup(func() { opWatchInterval, followRetryDelay, followRetryWindow = oi, od, ow })
}

// stagesOps reports a CA rotation one stage per poll, then Completed.
type stagesOps struct {
	ichorv1alpha1connect.OperationServiceClient
	stages []string
	calls  int
}

func (s *stagesOps) GetOperation(_ context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.GetOperationResponse], error) {
	op := &pb.Operation{Ref: req.Msg.GetOperation(), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Phase: ichorv1alpha1.TalosOperationPhaseInProgress}
	op.CaRotationStage = s.stages[min(s.calls, len(s.stages)-1)]
	s.calls++

	if op.CaRotationStage == ichorv1alpha1.CARotationStageDone {
		op.Phase = opPhaseDone
	}

	return connect.NewResponse(&pb.GetOperationResponse{Operation: op}), nil
}

// TestOperationWatchPrintsTheStagesAPollJumpedOver: operation get --watch prints the stages a poll
// skipped, between the ones it saw.
func TestOperationWatchPrintsTheStagesAPollJumpedOver(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true) // -o json: the progress lines of the machine formats

	ops := &stagesOps{stages: []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDone,
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	at := 0

	for _, stage := range []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
		ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping,
		ichorv1alpha1.CARotationStageFinalizing, ichorv1alpha1.CARotationStageDone,
	} {
		i := strings.Index(errw.String()[at:], ", stage "+stage)
		if i < 0 {
			t.Fatalf("no line for stage %s after position %d:\n%s", stage, at, errw.String())
		}

		at += i + len(", stage "+stage)
	}
}

// The same in the human style: a stage is the detail of the one step, and the last stage is the end line.
func TestOperationWatchHumanPrintsTheStagesAPollJumpedOver(t *testing.T) {
	shrinkFollowTimers(t)
	setHumanTestFlags(t, outputHuman)

	ops := &stagesOps{stages: []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDone,
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	at := 0

	for _, stage := range []string{
		ichorv1alpha1.CARotationStageAccepting, ichorv1alpha1.CARotationStageAwaitingCredentials,
		ichorv1alpha1.CARotationStageIssuing, ichorv1alpha1.CARotationStageDropping, ichorv1alpha1.CARotationStageFinalizing,
	} {
		i := strings.Index(errw.String()[at:], "· stage "+stage+"\n")
		if i < 0 {
			t.Fatalf("no line for stage %s after position %d:\n%s", stage, at, errw.String())
		}

		at += i
	}

	if !strings.HasSuffix(errw.String(), "✓ ca-rotation — Completed\n") {
		t.Errorf("the rotation must end with its Completed line:\n%s", errw.String())
	}
}

const installerRunning = "the installer Job is running"

// messagesOps reports an InProgress operation with one message per poll, then Completed.
type messagesOps struct {
	ichorv1alpha1connect.OperationServiceClient
	messages []string
	calls    int
}

func (m *messagesOps) GetOperation(_ context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.GetOperationResponse], error) {
	op := &pb.Operation{Ref: req.Msg.GetOperation(), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Phase: opPhaseDone}
	if m.calls < len(m.messages) {
		op.Phase, op.Message = ichorv1alpha1.TalosOperationPhaseInProgress, m.messages[m.calls]
	}

	m.calls++

	return connect.NewResponse(&pb.GetOperationResponse{Operation: op}), nil
}

// TestWatchPrintsTheOperationMessageWhenItChanges: --follow shows what an InProgress operation waits
// for, once per change.
func TestWatchPrintsTheOperationMessageWhenItChanges(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	ops := &messagesOps{messages: []string{
		installerRunning, installerRunning,
		"waiting for the ichor-ui Deployment to finish rolling out (0 of 1 updated replicas are available)",
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	for _, want := range []string{
		"phase InProgress: the installer Job is running",
		"phase InProgress: waiting for the ichor-ui Deployment to finish rolling out",
	} {
		if n := strings.Count(errw.String(), want); n != 1 {
			t.Fatalf("want %q printed once, got %d:\n%s", want, n, errw.String())
		}
	}
}

func TestWatchHumanPrintsTheOperationMessageWhenItChanges(t *testing.T) {
	shrinkFollowTimers(t)
	setHumanTestFlags(t, outputHuman)

	ops := &messagesOps{messages: []string{
		installerRunning, installerRunning,
		"waiting for the ichor-ui Deployment to finish rolling out (0 of 1 updated replicas are available)",
	}}
	c := &apiClients{Operation: ops, Close: func() {}}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}

	for _, want := range []string{
		"… ichor — InProgress · " + installerRunning + "\n",
		"… ichor — InProgress · waiting for the ichor-ui Deployment to finish rolling out",
	} {
		if n := strings.Count(errw.String(), want); n != 1 {
			t.Fatalf("want %q printed once, got %d:\n%s", want, n, errw.String())
		}
	}
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

// serverOps answers like an apiserver: every call to a dead server fails, a live one completes.
type serverOps struct {
	ichorv1alpha1connect.OperationServiceClient
	dead bool
}

func (s *serverOps) GetOperation(context.Context, *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.GetOperationResponse], error) {
	if s.dead {
		return nil, errors.New("dial tcp 10.0.0.1:6443: connect: connection refused")
	}

	return connect.NewResponse(&pb.GetOperationResponse{Operation: &pb.Operation{Phase: opPhaseDone}}), nil
}

func TestWatchFailsOverToAnotherControlPlane(t *testing.T) {
	shrinkFollowTimers(t)
	setAPITestFlags(t, true)

	var tried []string

	c := &apiClients{Operation: &serverOps{dead: true}, Close: func() {}, Server: cp1URL, Local: true}
	c.Dial = func(_ context.Context, server string) (*apiClients, error) {
		tried = append(tried, server)

		return &apiClients{Operation: &serverOps{dead: server == cp2URL}, Server: server, Close: func() {}}, nil
	}

	ring := []string{c.Server, cp2URL, cp3URL}
	at := 0
	c.Failover = func(ctx context.Context) (string, *apiClients, error) {
		at = (at + 1) % len(ring)
		nc, err := c.Dial(ctx, ring[at])

		return ring[at], nc, err
	}

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatalf("want success through another control plane, got %v", err)
	}

	if len(tried) != 2 || c.Server != cp3URL {
		t.Fatalf("tried %v, now on %s", tried, c.Server)
	}

	if !strings.Contains(errw.String(), "trying API server "+cp2URL) ||
		!strings.Contains(errw.String(), "API server "+cp3URL+" answered") {
		t.Fatalf("stderr %q", errw.String())
	}

	if c.Failover == nil || c.Dial == nil {
		t.Fatal("the failover hooks were lost when the clients were swapped")
	}
}

func TestControlPlaneServersKeepTheKubeconfigsPortAndSkipTheCurrentHost(t *testing.T) {
	machines := []*pb.Machine{
		{Endpoint: "10.0.0.1:50000", Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE},
		{Endpoint: "10.0.0.2", Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE},
		{Endpoint: "10.0.0.3", Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE},
		{Endpoint: "10.0.0.9", Role: pb.MachineRole_MACHINE_ROLE_WORKER},
		{Endpoint: "10.0.0.4", Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, Deleting: true},
	}

	got := controlPlaneServers(machines, cp1URL)
	if want := []string{cp2URL, cp3URL}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOtherServerKeepsTheOriginalCertificateName(t *testing.T) {
	cfg := &rest.Config{Host: "https://cluster.example:6443"}

	got := otherServer(cfg, cp2URL)
	if got.Host != cp2URL || got.ServerName != "cluster.example" {
		t.Fatalf("host %s serverName %q", got.Host, got.ServerName)
	}

	if cfg.Host != "https://cluster.example:6443" || cfg.ServerName != "" {
		t.Fatal("the original config was modified")
	}

	cfg.ServerName = "explicit-name"
	if otherServer(cfg, cp2URL).ServerName != "explicit-name" {
		t.Fatal("an explicit tls-server-name must win")
	}
}
