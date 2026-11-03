package autopatch

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

var now = time.Date(2026, 11, 4, 3, 0, 0, 0, time.UTC)

func targets() *ichorv1alpha1.IchorUpgradeTargets {
	at := metav1.NewTime(now.Add(-time.Hour))

	return &ichorv1alpha1.IchorUpgradeTargets{
		CheckedAt:  &at,
		Talos:      []string{"v1.15.0", "v1.14.5", "v1.14.4", "v1.14.3"},
		Kubernetes: []string{"v1.37.0", "v1.36.6", "v1.36.2", "v1.36.1"},
		Cilium:     []string{"v1.20.6", "v1.20.5"},
	}
}

// good is an input that starts the Talos patch; each case breaks one thing.
func good() Input {
	return Input{
		Now: now, Enabled: true, Targets: targets(),
		Cluster: Cluster{
			Talos: "v1.14.3", Kubernetes: "v1.36.1", CNIDetected: "cilium", CiliumVersion: "1.20.4", CiliumChart: "1.20.4",
			MachinesReady: true,
		},
	}
}

func TestDecideGuards(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		mod  func(*Input)
		skip string // substring of the reason; empty means it starts
		want Decision
	}{
		{name: "everything holds", want: Decision{Component: "talos", Target: "v1.14.5"}},
		{name: "off", mod: func(i *Input) { i.Enabled = false }, skip: "off"},
		{name: "halted", mod: func(i *Input) { i.Halted = true }, skip: "halted"},
		{name: "an Ichor upgrade runs", mod: func(i *Input) { i.IchorUpgrading = true }, skip: "Ichor upgrade"},
		{name: "outside the window", mod: func(i *Input) { i.Setting.Window = "04:00-05:00" }, skip: "outside the window"},
		{name: "inside the window", mod: func(i *Input) { i.Setting.Window = "02:00-04:00" }, want: Decision{Component: "talos", Target: "v1.14.5"}},
		{name: "inside a window that wraps midnight", mod: func(i *Input) { i.Setting.Window = "22:00-04:00" }, want: Decision{Component: "talos", Target: "v1.14.5"}},
		{name: "no lists", mod: func(i *Input) { i.Targets = nil }, skip: "has not listed"},
		{name: "lists never checked", mod: func(i *Input) { i.Targets.CheckedAt = nil }, skip: "has not listed"},
		{name: "stale lists", mod: func(i *Input) { at := metav1.NewTime(now.Add(-72 * time.Hour)); i.Targets.CheckedAt = &at }, skip: "older than"},
		{name: "an operation is unfinished", mod: func(i *Input) { i.Busy = "talos-abc" }, skip: "talos-abc"},
		{name: "the cluster lock is held", mod: func(i *Input) { i.LockHeld = true }, skip: "lock"},
		{name: "a machine is not Ready", mod: func(i *Input) { i.Cluster.MachinesReady = false }, skip: "Ready"},
		{name: "a start moments ago", mod: func(i *Input) { i.LastStart = now.Add(-time.Minute) }, skip: "less than"},
		{name: "a start long ago", mod: func(i *Input) { i.LastStart = now.Add(-time.Hour) }, want: Decision{Component: "talos", Target: "v1.14.5"}},
		{name: "only kubernetes selected", mod: func(i *Input) { i.Setting.Components = []string{"kubernetes"} }, want: Decision{Component: "kubernetes", Target: "v1.36.6"}},
		{name: "only cilium selected", mod: func(i *Input) { i.Setting.Components = []string{"cilium"} }, want: Decision{Component: "cilium", Target: "v1.20.6"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			in := good()
			if c.mod != nil {
				c.mod(&in)
			}

			got := Decide(in)
			if c.skip != "" {
				if got.Start() || !strings.Contains(got.Skip, c.skip) {
					t.Fatalf("got %+v, want a skip mentioning %q", got, c.skip)
				}

				return
			}

			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestDecideOrder: Talos, then Cilium, then Kubernetes, each only when the earlier has nothing.
func TestDecideOrder(t *testing.T) {
	t.Parallel()

	in := good()
	if got := Decide(in); got.Component != "talos" {
		t.Fatalf("first: %+v", got)
	}

	in.Cluster.Talos = "v1.14.5"
	if got := Decide(in); got.Component != "cilium" || got.Target != "v1.20.6" {
		t.Fatalf("after Talos is current: %+v", got)
	}

	in.Cluster.CiliumVersion = "1.20.6"
	if got := Decide(in); got.Component != "kubernetes" || got.Target != "v1.36.6" {
		t.Fatalf("after Cilium is current: %+v", got)
	}

	in.Cluster.Kubernetes = "v1.36.6"
	if got := Decide(in); got.Start() {
		t.Fatalf("everything current must start nothing: %+v", got)
	}
}

func TestCandidateNeverMinorsOrIncompatible(t *testing.T) {
	t.Parallel()

	c := good().Cluster
	tg := targets()

	// Only the next minor on offer: nothing to do.
	tg.Talos = []string{"v1.15.0"}
	tg.Kubernetes = []string{"v1.37.0"}
	tg.Cilium = []string{"v1.21.0"}

	for _, comp := range settings.AllAutoPatchComponents {
		if got := Candidate(comp, c, tg); got != "" {
			t.Errorf("%s: %q is a minor and must not be a candidate", comp, got)
		}
	}

	// A Kubernetes patch Cilium 1.19 is not tested with is refused by the table, not offered.
	tg = targets()
	c.CiliumVersion, c.CiliumChart = "1.19.8", "1.19.8"
	c.Kubernetes = "v1.35.1"
	tg.Kubernetes = []string{"v1.35.2"}

	if got := Candidate("kubernetes", c, tg); got != "v1.35.2" {
		t.Errorf("a compatible Kubernetes patch: %q", got)
	}

	// Cilium unknown for a Cilium cluster: no Kubernetes patch (nothing is known to be compatible).
	c.CiliumVersion = ""
	if got := Candidate("kubernetes", c, tg); got != "" {
		t.Errorf("unknown Cilium must offer no Kubernetes patch, got %q", got)
	}

	// Another CNI: the Cilium half does not constrain; Cilium itself has nothing.
	c.CNIDetected = "flannel"
	if got := Candidate("kubernetes", c, tg); got != "v1.35.2" {
		t.Errorf("a non-Cilium CNI does not constrain Kubernetes: %q", got)
	}

	if got := Candidate("cilium", c, targets()); got != "" {
		t.Errorf("no Cilium patch on a non-Cilium cluster: %q", got)
	}

	// The unmanaged preset: Ichor does not operate Cilium.
	c = good().Cluster
	c.CNIUnmanaged = true

	if got := Candidate("cilium", c, targets()); got != "" {
		t.Errorf("no Cilium patch on an unmanaged CNI: %q", got)
	}

	// A Cilium patch the embedded chart already reaches is not an offer.
	c = good().Cluster
	c.CiliumChart = "1.20.6"

	if got := Candidate("cilium", c, targets()); got != "" {
		t.Errorf("a patch the chart reaches: %q", got)
	}

	// The newest wins even when the release set's version is older.
	c = good().Cluster
	if got := Candidate("talos", c, targets()); got != "v1.14.5" {
		t.Errorf("newest patch: %q", got)
	}
}

func TestResume(t *testing.T) {
	t.Parallel()

	created := now.Add(-time.Hour)
	live := &Halting{Cluster: "c", Created: created}

	for _, c := range []struct {
		name        string
		halted, ack string
		op          *Halting
		later       []LaterOperation
		wantResumed bool
	}{
		{name: "not halted", halted: "", wantResumed: false},
		{name: "still halted", halted: "ns/op", op: live, wantResumed: false},
		{name: "acknowledged", halted: "ns/op", ack: "ns/op", op: live, wantResumed: true},
		{name: "an acknowledgement of an older halt does not count", halted: "ns/op2", ack: "ns/op", op: live, wantResumed: false},
		{name: "the operation completed after a manual resume", halted: "ns/op", op: &Halting{Cluster: "c", Created: created, Completed: true}, wantResumed: true},
		{name: "the operation was deleted", halted: "ns/op", op: nil, wantResumed: true},
		{name: "a later manual upgrade completed", halted: "ns/op", op: live,
			later: []LaterOperation{{Cluster: "c", Created: now, Completed: true}}, wantResumed: true},
		{name: "a later automatic one does not count", halted: "ns/op", op: live,
			later: []LaterOperation{{Cluster: "c", Created: now, Completed: true, Auto: true}}, wantResumed: false},
		{name: "a later manual one that did not complete does not count", halted: "ns/op", op: live,
			later: []LaterOperation{{Cluster: "c", Created: now}}, wantResumed: false},
		{name: "a manual one of another cluster does not count", halted: "ns/op", op: live,
			later: []LaterOperation{{Cluster: "other", Created: now, Completed: true}}, wantResumed: false},
		{name: "an earlier manual one does not count", halted: "ns/op", op: live,
			later: []LaterOperation{{Cluster: "c", Created: created.Add(-time.Hour), Completed: true}}, wantResumed: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := Resume(c.halted, c.ack, c.op, c.later); got != c.wantResumed {
				t.Fatalf("Resume = %v, want %v", got, c.wantResumed)
			}
		})
	}
}
