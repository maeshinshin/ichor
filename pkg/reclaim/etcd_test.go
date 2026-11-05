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

package reclaim

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"google.golang.org/grpc"
)

// blockingEtcd is a Talos node whose etcd is not running: the call neither answers nor fails, it
// waits until its context ends (as `talosctl etcd members` does against such a node).
type blockingEtcd struct{ calls int }

func (b *blockingEtcd) EtcdMemberList(ctx context.Context, _ *machine.EtcdMemberListRequest, _ ...grpc.CallOption) (*machine.EtcdMemberListResponse, error) {
	b.calls++

	<-ctx.Done()

	return nil, ctx.Err()
}

func (b *blockingEtcd) EtcdStatus(ctx context.Context, _ ...grpc.CallOption) (*machine.EtcdStatusResponse, error) {
	b.calls++

	<-ctx.Done()

	return nil, ctx.Err()
}

// refusingEtcd answers at once with an error, as a node whose etcd service is stopped may.
type refusingEtcd struct{ blockingEtcd }

func (*refusingEtcd) EtcdMemberList(context.Context, *machine.EtcdMemberListRequest, ...grpc.CallOption) (*machine.EtcdMemberListResponse, error) {
	return nil, errors.New("etcd is not running")
}

// A control plane whose etcd does not answer does not hang the inspection: the call is cut off after
// the timeout and the result is an explicit "etcd unavailable" that says how long it waited.
func TestInspectEtcdIsBoundedWhenEtcdDoesNotAnswer(t *testing.T) {
	var (
		api = &blockingEtcd{}
		in  = Inspection{ControlPlane: true}
	)

	done := make(chan struct{})

	go func() {
		defer close(done)

		inspectEtcd(context.Background(), api, 50*time.Millisecond, &in)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inspectEtcd did not return: the etcd call has no timeout")
	}

	if in.EtcdKnown || in.ClusterReachable || len(in.EtcdMembers) != 0 {
		t.Fatalf("an unanswered etcd produced a membership: %+v", in)
	}

	if !strings.Contains(in.EtcdProblem, "did not answer within 50ms") {
		t.Fatalf("EtcdProblem does not say the call timed out: %q", in.EtcdProblem)
	}

	if api.calls != 1 {
		t.Fatalf("want one etcd call (the status read needs the member list), got %d", api.calls)
	}
}

func TestInspectEtcdRecordsAnImmediateError(t *testing.T) {
	in := Inspection{ControlPlane: true}

	inspectEtcd(context.Background(), &refusingEtcd{}, time.Second, &in)

	if in.EtcdKnown || !strings.Contains(in.EtcdProblem, "etcd is not running") {
		t.Fatalf("got %+v", in)
	}
}

func etcdDownInspection() Inspection {
	return Inspection{
		Address: "10.0.0.5", UUID: testUUID, ClusterName: "old", ClusterID: "cid", ControlPlane: true,
		EtcdProblem: "etcd member list did not answer within 20s",
	}
}

// the "the foreign cluster does not exist" case: a control plane whose etcd is not running is
// reset ungracefully when the operator says so, with a warning that no quorum check was possible.
func TestAssessEtcdDownProceedsWithForceUngraceful(t *testing.T) {
	p := Assess(etcdDownInspection(), Known{}, Options{ForceUngraceful: true})

	if err := p.Refused(); err != nil {
		t.Fatalf("refused: %v", err)
	}

	if p.Graceful {
		t.Fatal("a graceful reset was planned for a cluster whose etcd is down")
	}

	if p.NeedsForce {
		t.Fatal("--force is for a quorum break; there is no membership to break")
	}

	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "etcd") || !strings.Contains(p.Warnings[0], "did not answer within 20s") {
		t.Fatalf("want one warning that names etcd and why, got %v", p.Warnings)
	}
}

// Without --force-ungraceful the refusal names etcd, not only the cluster, so the operator knows why.
func TestAssessEtcdDownRefusesAGracefulReset(t *testing.T) {
	p := Assess(etcdDownInspection(), Known{}, Options{})

	err := p.Refused()
	if err == nil {
		t.Fatal("a graceful reset of an unreachable cluster was allowed")
	}

	for _, want := range []string{"etcd", "did not answer within 20s", "--force-ungraceful"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

// A worker has no etcd, so a leftover EtcdProblem (never set for one) changes nothing for it.
func TestAssessWorkerIgnoresEtcdProblem(t *testing.T) {
	in := etcdDownInspection()
	in.ControlPlane = false
	in.ClusterReachable = true

	p := Assess(in, Known{}, Options{})
	if err := p.Refused(); err != nil || len(p.Warnings) != 0 {
		t.Fatalf("got refusal %v, warnings %v", err, p.Warnings)
	}
}
