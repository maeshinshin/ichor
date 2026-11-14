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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	blockres "github.com/siderolabs/talos/pkg/machinery/resources/block"
	"google.golang.org/protobuf/proto"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const testUUID = "AAAA-1"

// userDiskSDB is a second disk the user-disk wipe tests name.
const userDiskSDB = "/dev/sdb"

const testOldClusterID = "cid-old"

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

const testAaaaUUID = "aaaa-1"

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
			name: "already an Ichor node", in: worker(), refused: "already an Ichor node",
			known: Known{MachineUUIDs: []string{testAaaaUUID}},
		},
		{
			name: "an approved machine is pointed at Remove node", in: worker(), refused: "Remove node",
			known: Known{MachineUUIDs: []string{testAaaaUUID}, ClusterIDs: []string{testOldClusterID}},
		},
		{
			// No approved TalosMachine has this UUID, so the node is an orphan of the cluster and
			// may be wiped. Neither refusal points at adopt: that would loop with adopt's own.
			name: "an orphan of a managed cluster is reclaimable", in: worker(), graceful: true,
			known: Known{ClusterIDs: []string{testOldClusterID}},
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

func TestExecute(t *testing.T) {
	ok := Confirmation{UUID: testUUID}

	for _, tc := range []struct {
		name    string
		node    *fakeNode
		known   Known
		opt     Options
		conf    Confirmation
		wantErr bool
		resets  []bool
	}{
		{
			name: "nothing without a matching UUID", node: &fakeNode{in: worker()},
			conf: Confirmation{UUID: "wrong"}, wantErr: true,
		},
		{
			name: "refuses an Ichor machine", node: &fakeNode{in: worker()}, conf: ok, wantErr: true,
			known: Known{MachineUUIDs: []string{testUUID}},
		},
		{name: "graceful reset", node: &fakeNode{in: worker()}, conf: ok, resets: []bool{true}},
		{
			name: "ungraceful only when forced", node: &fakeNode{in: unreachable()}, conf: ok,
			opt: Options{ForceUngraceful: true}, resets: []bool{false},
		},
		{
			name: "wraps a reset failure", node: &fakeNode{in: worker(), resetErr: errors.New("boom")}, conf: ok,
			wantErr: true, resets: []bool{true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Execute(context.Background(), tc.node, tc.known, tc.opt, tc.conf)
			if (err != nil) != tc.wantErr || !slices.Equal(tc.node.resets, tc.resets) {
				t.Fatalf("err = %v (want error %v), resets = %v, want %v", err, tc.wantErr, tc.node.resets, tc.resets)
			}
		})
	}
}

func TestWaitMaintenance(t *testing.T) {
	calls := 0
	probe := func(context.Context, string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("connection refused")
		}

		return testAaaaUUID, nil
	}

	if err := WaitMaintenance(context.Background(), "10.0.0.5", testUUID, time.Millisecond, probe); err != nil {
		t.Fatal(err)
	}

	other := func(context.Context, string) (string, error) { return "zzzz", nil }
	if err := WaitMaintenance(context.Background(), "10.0.0.5", testUUID, time.Millisecond, other); err == nil ||
		!strings.Contains(err.Error(), "another node") {
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

// A wipe mode asks Talos to erase the whole system disk (boot and EFI included), so the node can
// be installed again from a boot image; without one the request stays the STATE and EPHEMERAL
// wipe.
func TestResetRequestWipeMode(t *testing.T) {
	if got, want := ResetRequestWipe(false, "", nil), ResetRequest(false); !proto.Equal(got, want) {
		t.Fatalf("no mode must be the request: %+v", got)
	}

	for mode, want := range map[string]machine.ResetRequest_WipeMode{
		ichorv1alpha1.WipeModeSystemDisk: machine.ResetRequest_SYSTEM_DISK,
		ichorv1alpha1.WipeModeAll:        machine.ResetRequest_ALL,
	} {
		r := ResetRequestWipe(false, mode, nil)
		if r.Mode != want || r.Graceful || !r.Reboot {
			t.Fatalf("%s: mode/graceful/reboot wrong: %+v", mode, r)
		}

		// An empty list is what makes Talos wipe the whole system disk.
		if len(r.SystemPartitionsToWipe) != 0 || len(r.UserDisksToWipe) != 0 {
			t.Fatalf("%s: partitions or user disks named: %+v", mode, r)
		}
	}
}

// system-disk-and-user-disks is all plus the devices it names: Talos erases a user disk only when it is listed,
// and all lists none, so all never erases one.
func TestResetRequestWipeUserDisks(t *testing.T) {
	disks := []string{userDiskSDB, "/dev/sdc"}

	r := ResetRequestWipe(false, ichorv1alpha1.WipeModeSystemDiskAndUserDisks, disks)
	if r.Mode != machine.ResetRequest_ALL || r.Graceful || !r.Reboot ||
		len(r.SystemPartitionsToWipe) != 0 || !slices.Equal(r.UserDisksToWipe, disks) {
		t.Fatalf("system-disk-and-user-disks must be all with the disks named: %+v", r)
	}

	if r := ResetRequestWipe(false, ichorv1alpha1.WipeModeAll, disks); len(r.UserDisksToWipe) != 0 {
		t.Fatalf("all must not name user disks: %+v", r)
	}
}

// UserDiskPaths names every disk but the system disk, CD-ROM drives and read-only disks, and refuses to
// answer when no system disk is known, since then nothing can be kept apart from the system disk.
func TestUserDiskPaths(t *testing.T) {
	ctx := context.Background()
	st := state.WrapCore(inmem.NewState())

	if _, err := UserDiskPaths(ctx, st); err == nil {
		t.Fatal("no system disk must be an error, not every disk")
	}

	sys := blockres.NewSystemDisk(blockres.NamespaceName, blockres.SystemDiskID)
	sys.TypedSpec().DiskID, sys.TypedSpec().DevPath = "sda", "/dev/sda"

	if err := st.Create(ctx, sys); err != nil {
		t.Fatal(err)
	}

	if err := st.Create(ctx, blockres.NewDevice(blockres.NamespaceName, "sda")); err != nil {
		t.Fatal(err)
	}

	for _, d := range []struct {
		id, path  string
		cdrom, ro bool
	}{
		{"sda", "/dev/sda", false, false},
		{"sdb", userDiskSDB, false, false},
		{"sdc", "/dev/sdc", true, false},
		{"sdd", "/dev/sdd", false, true},
		{"sde", "/dev/sde", false, false},
	} {
		disk := blockres.NewDisk(blockres.NamespaceName, d.id)
		disk.TypedSpec().DevPath, disk.TypedSpec().CDROM, disk.TypedSpec().Readonly = d.path, d.cdrom, d.ro

		if err := st.Create(ctx, disk); err != nil {
			t.Fatal(err)
		}
	}

	got, err := UserDiskPaths(ctx, st)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{userDiskSDB, "/dev/sde"}; !slices.Equal(got, want) {
		t.Fatalf("user disks = %q, want %q", got, want)
	}
}

func TestAssess_OrphanOfManagedClusterWarnsAndDoesNotLoop(t *testing.T) {
	managed := Known{ClusterIDs: []string{testOldClusterID}, ClusterNames: map[string]string{testOldClusterID: "prod-a"}}
	p := Assess(worker(), managed, Options{})

	if err := p.Refused(); err != nil {
		t.Fatalf("an orphan must not be refused: %v", err)
	}

	w := strings.Join(p.Warnings, ";")
	if !strings.Contains(w, "--cluster prod-a --add-machine") || strings.Contains(w, "NAME") {
		t.Fatalf("the hint must name the TalosCluster, got %v", p.Warnings)
	}

	if w := strings.Join(p.Warnings, ";"); !strings.Contains(w, "manages") || !strings.Contains(w, "--add-machine") {
		t.Fatalf("want a warning that names the cluster and the keep-it alternative, got %v", p.Warnings)
	}

	known := Known{MachineUUIDs: []string{testAaaaUUID}, ClusterIDs: []string{testOldClusterID}}
	refused := Assess(worker(), known, Options{}).Refusals
	if r := strings.Join(refused, ";"); strings.Contains(r, "--add-machine") || strings.Contains(r, "adopt") {
		t.Fatalf("the refusal of an approved machine must not send the user to adopt: %v", refused)
	}
}
