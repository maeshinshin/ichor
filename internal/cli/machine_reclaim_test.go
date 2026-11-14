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

	"github.com/maeshinshin/ichor/pkg/reclaim"
)

const testUUID = "AAAA-1"

const reclaimMarker = "TALOSCONFIG-MARKER-77"

type reclaimFakeNode struct {
	in     reclaim.Inspection
	resets []bool
}

func (f *reclaimFakeNode) Inspect(context.Context) (reclaim.Inspection, error) { return f.in, nil }
func (f *reclaimFakeNode) Close() error                                        { return nil }
func (f *reclaimFakeNode) Reset(_ context.Context, g bool) error {
	f.resets = append(f.resets, g)

	return nil
}

type reclaimHarness struct {
	node       *reclaimFakeNode
	out        bytes.Buffer
	registered []string
	deps       reclaimDeps
}

func newReclaimHarness(stdin string, known reclaim.Known) *reclaimHarness {
	h := &reclaimHarness{node: &reclaimFakeNode{in: reclaim.Inspection{
		Address: "10.0.0.5", UUID: testUUID, Hostname: "w1", ClusterName: "old", ClusterID: "cid", ClusterReachable: true,
		Volumes: []reclaim.Volume{{Name: "data", Location: "/dev/sdb1", Size: 10, Encrypted: true}},
	}}}

	h.deps = reclaimDeps{
		Credential: reclaim.NewCredential([]byte(reclaimMarker)),
		Dial: func(context.Context, reclaim.Credential, string, string) (reclaim.Node, error) {
			return h.node, nil
		},
		Known: func(context.Context) (reclaim.Known, error) { return known, nil },
		Probe: func(context.Context, string) (string, error) { return strings.ToLower(testUUID), nil },
		Register: func(_ context.Context, a string) error {
			h.registered = append(h.registered, a)

			return nil
		},
		In: strings.NewReader(stdin), Out: &h.out, Poll: time.Millisecond,
	}

	return h
}

func TestMachineReclaimTypedUUIDResets(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{})

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: time.Second, Cluster: "new"}); err != nil {
		t.Fatal(err)
	}

	if len(h.node.resets) != 1 || !h.node.resets[0] {
		t.Fatalf("want one graceful reset, got %v", h.node.resets)
	}

	if len(h.registered) != 1 || h.registered[0] != "10.0.0.5" {
		t.Fatalf("want the address registered, got %v", h.registered)
	}

	out := h.out.String()
	for _, want := range []string{"ENCRYPTED", "/dev/sdb1", `"old"`, testUUID} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, reclaimMarker) {
		t.Fatal("the talosconfig was printed")
	}
}

// TestMachineReclaimYesDoesNotSatisfyConfirmation: the global --yes must not stand in for the UUID.
// The reset is sent without a probe: with --wait 0 the output must say it is not confirmed, never that
// the machine is back in maintenance mode.
func TestMachineReclaimNoWaitDoesNotClaimTheWipe(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{})

	probed := false
	h.deps.Probe = func(context.Context, string) (string, error) {
		probed = true

		return strings.ToLower(testUUID), nil
	}

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: 0}); err != nil {
		t.Fatal(err)
	}

	if probed {
		t.Error("--wait 0 probed the node")
	}

	if out := h.out.String(); !strings.Contains(out, "not verified") {
		t.Errorf("output does not say the reset is unverified:\n%s", out)
	}
}

func TestMachineReclaimYesDoesNotSatisfyConfirmation(t *testing.T) {
	old := assumeYes
	assumeYes = true

	t.Cleanup(func() { assumeYes = old })

	for name, stdin := range map[string]string{"empty input": "", "y": "y\n", "wrong uuid": "BBBB-2\n"} {
		t.Run(name, func(t *testing.T) {
			h := newReclaimHarness(stdin, reclaim.Known{})

			if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: time.Second}); err == nil {
				t.Fatal("want an error")
			}

			if len(h.node.resets) != 0 {
				t.Fatal("a reset was sent without the typed UUID")
			}
		})
	}
}

func TestMachineReclaimConfirmFlag(t *testing.T) {
	h := newReclaimHarness("", reclaim.Known{})

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: strings.ToLower(testUUID), Wait: time.Second}); err != nil {
		t.Fatal(err)
	}

	if len(h.node.resets) != 1 {
		t.Fatalf("resets = %v", h.node.resets)
	}
}

func TestMachineReclaimRefusesIchorMachine(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{MachineUUIDs: []string{strings.ToLower(testUUID)}})

	err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID})
	if err == nil || !strings.Contains(err.Error(), "already an Ichor node") {
		t.Fatalf("err = %v", err)
	}

	if len(h.node.resets) != 0 {
		t.Fatal("reset sent")
	}
}

func TestMachineReclaimQuorumNeedsForceAndClusterName(t *testing.T) {
	mk := func() *reclaimHarness {
		h := newReclaimHarness("", reclaim.Known{})
		h.node.in.ControlPlane, h.node.in.EtcdKnown = true, true
		h.node.in.EtcdMembers = []reclaim.EtcdMember{{ID: 1}}

		return h
	}

	h := mk()
	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID}); err == nil || len(h.node.resets) != 0 {
		t.Fatalf("last control plane must be refused: %v", err)
	}

	h = mk()
	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID, Force: true}); err == nil || len(h.node.resets) != 0 {
		t.Fatalf("--force without the cluster name must not reset: %v", err)
	}

	h = mk()
	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5",
		reclaimFlags{Confirm: testUUID, Force: true, ConfirmCluster: "old"}); err != nil || len(h.node.resets) != 1 {
		t.Fatalf("forced reset failed: %v %v", err, h.node.resets)
	}
}

func TestMachineReclaimCannotVerifyIchorFleet(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{})
	h.deps.Known = func(context.Context) (reclaim.Known, error) { return reclaim.Known{}, errors.New("no cluster") }

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{}); err == nil || len(h.node.resets) != 0 {
		t.Fatalf("must refuse when Ichor's machines cannot be listed: %v", err)
	}
}

func TestMachineReclaimAddressChangeGivesHint(t *testing.T) {
	h := newReclaimHarness("AAAA-1\n", reclaim.Known{})
	h.deps.Probe = func(context.Context, string) (string, error) { return "", errors.New("no route") }

	err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Wait: 20 * time.Millisecond, Cluster: "new"})
	if err == nil {
		t.Fatal("want an error")
	}

	if !strings.Contains(h.out.String(), "new address from DHCP") || len(h.registered) != 0 {
		t.Fatalf("want the DHCP hint and no registration:\n%s", h.out.String())
	}
}

// A control plane whose etcd is not running is
// refused without --force-ungraceful, and reset with it; the report says why there is no etcd membership.
func TestMachineReclaimEtcdDown(t *testing.T) {
	mk := func() *reclaimHarness {
		h := newReclaimHarness("", reclaim.Known{})
		h.node.in.ControlPlane, h.node.in.ClusterReachable = true, false
		h.node.in.EtcdProblem = "etcd member list did not answer within 20s"

		return h
	}

	h := mk()
	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID, ForceUngraceful: true}); err != nil {
		t.Fatalf("%v\n%s", err, h.out.String())
	}

	if len(h.node.resets) != 1 || h.node.resets[0] {
		t.Fatalf("want one ungraceful reset, got %v", h.node.resets)
	}

	for _, want := range []string{"Inspect", "gives up after", "not running or not answering", "treated as not existing"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.out.String())
		}
	}

	h = mk()

	err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID})
	if err == nil || !strings.Contains(err.Error(), "--force-ungraceful") || len(h.node.resets) != 0 {
		t.Fatalf("err = %v, resets = %v", err, h.node.resets)
	}
}
