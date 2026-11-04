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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
)

const testUUID = "AAAA-1"

const marker = "TOPSECRET-MARKER-9f3a"

func TestCredentialNeverPrints(t *testing.T) {
	c := NewCredential([]byte(marker))

	var buf bytes.Buffer

	fmt.Fprintf(&buf, "%v|%+v|%#v|%s|%q|%d", c, c, c, c, c, c)
	fmt.Fprintf(&buf, "|%v", struct{ C Credential }{c})
	fmt.Fprintf(&buf, "|%+v", &struct{ C *Credential }{&c})

	j, err := json.Marshal(struct{ C Credential }{c})
	if err != nil {
		t.Fatal(err)
	}

	buf.Write(j)

	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "cred", c)

	if strings.Contains(buf.String(), marker) {
		t.Fatalf("credential leaked: %s", buf.String())
	}

	if string(c.Bytes()) != marker {
		t.Fatal("Bytes must still return the content")
	}
}

func TestDialErrorDoesNotQuoteCredential(t *testing.T) {
	_, err := Dial(context.Background(), NewCredential([]byte("context: "+marker+"\n\t{bad")), "10.0.0.1", "")
	if err == nil {
		t.Fatal("want an error")
	}

	if strings.Contains(err.Error(), marker) {
		t.Fatalf("error quotes the credential: %v", err)
	}
}

func TestResetRequest(t *testing.T) {
	for _, graceful := range []bool{true, false} {
		r := ResetRequest(graceful)
		if r.Graceful != graceful || !r.Reboot {
			t.Fatalf("graceful/reboot wrong: %+v", r)
		}

		if r.Mode != machine.ResetRequest_ALL {
			t.Fatalf("Mode must stay the default (USER_DISKS is rejected next to named partitions): %v", r.Mode)
		}

		labels := make([]string, 0, 2)
		for _, p := range r.SystemPartitionsToWipe {
			if !p.Wipe {
				t.Fatalf("partition %q not wiped", p.Label)
			}

			labels = append(labels, p.Label)
		}

		if strings.Join(labels, ",") != "STATE,EPHEMERAL" {
			t.Fatalf("want exactly STATE and EPHEMERAL, got %v", labels)
		}
	}
}

func worker() Inspection {
	return Inspection{
		Address: "10.0.0.5", UUID: testUUID, Hostname: "w1", ClusterName: "old", ClusterID: "cid-old", ClusterReachable: true,
	}
}

func unreachable() Inspection {
	i := worker()
	i.ClusterReachable = false

	return i
}

func blindEtcd() Inspection {
	i := controlPlane(0)
	i.EtcdKnown = false

	return i
}

func controlPlane(members int) Inspection {
	in := worker()
	in.ControlPlane, in.EtcdKnown = true, true

	for i := range members {
		in.EtcdMembers = append(in.EtcdMembers, EtcdMember{ID: uint64(i + 1)})
	}

	return in
}

// unhealthy marks the last n members unhealthy; the target is member 1.
func unhealthy(in Inspection, n int) Inspection {
	in.LocalMemberID = 1

	for i := range n {
		in.EtcdMembers[len(in.EtcdMembers)-1-i].Unhealthy = true
	}

	return in
}

func TestAssess(t *testing.T) {
	tests := []struct {
		name      string
		in        Inspection
		known     Known
		opt       Options
		refused   string // substring of a refusal, "" for none
		graceful  bool
		needForce bool
	}{
		{name: "worker of a reachable cluster is graceful", in: worker(), graceful: true},
		{
			name: "already an Ichor machine", in: worker(), refused: "already an Ichor machine",
			known: Known{MachineUUIDs: []string{"aaaa-1"}},
		},
		{
			name: "already an Ichor cluster", in: worker(), refused: "already manages",
			known: Known{ClusterIDs: []string{"cid-old"}},
		},
		{
			name: "a managed cluster's node is pointed at --add-machine", in: worker(), refused: "ichor adopt --cluster",
			known: Known{ClusterIDs: []string{"cid-old"}},
		},
		{name: "no uuid", in: func() Inspection { i := worker(); i.UUID = ""; return i }(), refused: "no UUID"},
		{name: "unreachable cluster refused",
			in: unreachable(), refused: "--force-ungraceful"},
		{
			name: "unreachable with force-ungraceful", in: unreachable(),
			opt: Options{ForceUngraceful: true},
		},
		{name: "3 member control plane ok", in: controlPlane(3), graceful: true},
		{name: "2 member control plane breaks quorum", in: controlPlane(2), refused: "loses quorum"},
		{name: "3 members, other two unhealthy", in: unhealthy(controlPlane(3), 2), refused: "unhealthy"},
		{name: "3 members, one other unhealthy", in: unhealthy(controlPlane(3), 1), refused: "unhealthy"},
		{name: "3 members, only the target unhealthy", in: func() Inspection {
			i := controlPlane(3)
			i.LocalMemberID = 1
			i.EtcdMembers[0].Unhealthy = true

			return i
		}(), graceful: true},
		{
			name: "3 members two unhealthy forced", in: unhealthy(controlPlane(3), 2),
			opt: Options{Force: true}, graceful: true, needForce: true,
		},
		{name: "last control plane", in: controlPlane(1), refused: "last control plane"},
		{name: "last control plane forced", in: controlPlane(1), opt: Options{Force: true}, graceful: true, needForce: true},
		{name: "unreadable etcd is not a quorum verdict", in: blindEtcd(), graceful: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Assess(tt.in, tt.known, tt.opt)

			if tt.refused == "" && len(p.Refusals) != 0 {
				t.Fatalf("unexpected refusals: %v", p.Refusals)
			}

			if tt.refused != "" && !strings.Contains(strings.Join(p.Refusals, ";"), tt.refused) {
				t.Fatalf("want refusal containing %q, got %v", tt.refused, p.Refusals)
			}

			if tt.refused == "" && p.Graceful != tt.graceful {
				t.Fatalf("graceful = %v, want %v", p.Graceful, tt.graceful)
			}

			if p.NeedsForce != tt.needForce {
				t.Fatalf("needsForce = %v, want %v", p.NeedsForce, tt.needForce)
			}
		})
	}
}

func TestConfirmationCheck(t *testing.T) {
	in := controlPlane(1)
	forced := Assess(in, Known{}, Options{Force: true})
	plain := Assess(worker(), Known{}, Options{})

	for name, c := range map[string]struct {
		c    Confirmation
		in   Inspection
		p    Plan
		fail bool
	}{
		"exact uuid":                {Confirmation{UUID: testUUID}, worker(), plain, false},
		"case and space tolerated":  {Confirmation{UUID: " aaaa-1\n"}, worker(), plain, false},
		"empty":                     {Confirmation{}, worker(), plain, true},
		"wrong":                     {Confirmation{UUID: "BBBB-2"}, worker(), plain, true},
		"forced needs cluster name": {Confirmation{UUID: testUUID}, in, forced, true},
		"forced wrong cluster name": {Confirmation{UUID: testUUID, ClusterName: "nope"}, in, forced, true},
		"forced with cluster name":  {Confirmation{UUID: testUUID, ClusterName: "old"}, in, forced, false},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.c.Check(c.in, c.p); (err != nil) != c.fail {
				t.Fatalf("err = %v, want failure %v", err, c.fail)
			}
		})
	}
}

type fakeNode struct {
	in       Inspection
	inspects int
	resets   []bool
	resetErr error
}

func (f *fakeNode) Inspect(context.Context) (Inspection, error) { f.inspects++; return f.in, nil }
func (f *fakeNode) Close() error                                { return nil }
func (f *fakeNode) Reset(_ context.Context, graceful bool) error {
	f.resets = append(f.resets, graceful)

	return f.resetErr
}

func TestExecuteSendsNothingWithoutConfirmation(t *testing.T) {
	n := &fakeNode{in: worker()}

	if _, err := Execute(context.Background(), n, Known{}, Options{}, Confirmation{UUID: "wrong"}); err == nil {
		t.Fatal("want an error")
	}

	if len(n.resets) != 0 {
		t.Fatal("a reset was sent without a matching UUID")
	}
}

func TestExecuteRefusesAnIchorMachine(t *testing.T) {
	n := &fakeNode{in: worker()}

	known := Known{MachineUUIDs: []string{testUUID}}
	_, err := Execute(context.Background(), n, known, Options{}, Confirmation{UUID: testUUID})
	if err == nil || len(n.resets) != 0 {
		t.Fatalf("err = %v, resets = %v", err, n.resets)
	}
}

func TestExecuteSendsGracefulReset(t *testing.T) {
	n := &fakeNode{in: worker()}

	if _, err := Execute(context.Background(), n, Known{}, Options{}, Confirmation{UUID: testUUID}); err != nil {
		t.Fatal(err)
	}

	if len(n.resets) != 1 || !n.resets[0] {
		t.Fatalf("want one graceful reset, got %v", n.resets)
	}
}

func TestExecuteUngracefulOnlyWhenForced(t *testing.T) {
	n := &fakeNode{in: unreachable()}

	opt := Options{ForceUngraceful: true}
	if _, err := Execute(context.Background(), n, Known{}, opt, Confirmation{UUID: testUUID}); err != nil {
		t.Fatal(err)
	}

	if len(n.resets) != 1 || n.resets[0] {
		t.Fatalf("want one ungraceful reset, got %v", n.resets)
	}
}

func TestExecuteWrapsResetFailure(t *testing.T) {
	n := &fakeNode{in: worker(), resetErr: errors.New("boom")}

	if _, err := Execute(context.Background(), n, Known{}, Options{}, Confirmation{UUID: testUUID}); err == nil {
		t.Fatal("want an error")
	}
}

func TestWaitMaintenance(t *testing.T) {
	calls := 0
	probe := func(context.Context, string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("connection refused")
		}

		return "aaaa-1", nil
	}

	if err := WaitMaintenance(context.Background(), "10.0.0.5", testUUID, time.Millisecond, probe); err != nil {
		t.Fatal(err)
	}

	other := func(context.Context, string) (string, error) { return "zzzz", nil }
	if err := WaitMaintenance(context.Background(), "10.0.0.5", testUUID, time.Millisecond, other); err == nil ||
		!strings.Contains(err.Error(), "another machine") {
		t.Fatalf("want a different-machine error, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	never := func(context.Context, string) (string, error) { return "", errors.New("down") }
	err := WaitMaintenance(ctx, "10.0.0.5", testUUID, time.Millisecond, never)
	if err == nil || !strings.Contains(err.Error(), "down") {
		t.Fatalf("want a timeout carrying the last probe error, got %v", err)
	}
}
