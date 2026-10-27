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
	"net"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestPlanRestoreNodes pins who is wiped and who recovers: the controller's own control plane is held
// back (wiping it stops the controller), unless it is the only one.
func TestPlanRestoreNodes(t *testing.T) {
	mk := func(name, host string) *ichorv1alpha1.TalosMachine {
		return &ichorv1alpha1.TalosMachine{Name: name, Spec: ichorv1alpha1.TalosMachineSpec{Hostname: host}}
	}

	cases := []struct {
		name       string
		cps        []*ichorv1alpha1.TalosMachine
		node       string
		wantOrder  []string
		wantTarget string
		wantOwn    string
		solo       bool
	}{
		{"controller elsewhere", []*ichorv1alpha1.TalosMachine{mk("b", "hb"), mk("a", "ha"), mk("c", "hc")}, "w1", []string{"a", "b", "c"}, "a", "", false},
		{"controller outside the cluster", []*ichorv1alpha1.TalosMachine{mk("b", "hb"), mk("a", "ha")}, "", []string{"a", "b"}, "a", "", false},
		{"controller on a control plane", []*ichorv1alpha1.TalosMachine{mk("b", "hb"), mk("a", "ha"), mk("c", "hc")}, "ha", []string{"b", "c"}, "b", "a", false},
		{"controller on the only control plane", []*ichorv1alpha1.TalosMachine{mk("a", "ha")}, "ha", []string{"a"}, "a", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := planRestoreNodes(tc.cps, tc.node)

			var order []string
			for _, m := range p.order {
				order = append(order, m.Name)
			}

			if !slices.Equal(order, tc.wantOrder) {
				t.Fatalf("order %v, want %v", order, tc.wantOrder)
			}

			if p.target.Name != tc.wantTarget {
				t.Fatalf("target %s, want %s", p.target.Name, tc.wantTarget)
			}

			if (p.own == nil) != (tc.wantOwn == "") || (p.own != nil && p.own.Name != tc.wantOwn) {
				t.Fatalf("own %v, want %q", p.own, tc.wantOwn)
			}

			if p.controllerOnTarget != tc.solo {
				t.Fatalf("controllerOnTarget %v, want %v", p.controllerOnTarget, tc.solo)
			}
		})
	}
}

// TestRestoreTailsRunsOnceAndIsWaitedFor is the registry main waits on after the manager stopped.
func TestRestoreTailsRunsOnceAndIsWaitedFor(t *testing.T) {
	tails := &restoreTails{running: map[types.NamespacedName]bool{}, inflight: map[types.NamespacedName]types.NamespacedName{}}
	key := types.NamespacedName{Namespace: "ns", Name: "op"}
	backup := types.NamespacedName{Namespace: "ns", Name: "bk"}

	var runs atomic.Int32

	release := make(chan struct{})
	run := func() {
		runs.Add(1)
		<-release
	}

	tails.start(key, backup, run)
	tails.start(key, backup, run) // the same operation twice: one goroutine

	if !tails.isRunning(key) {
		t.Fatal("the job is not reported running")
	}

	if b, ok := tails.backupOf(key); !ok || b.Name != "bk" {
		t.Fatalf("backupOf: %v %v", b, ok)
	}

	done := make(chan struct{})

	go func() { tails.wg.Wait(); close(done) }()

	select {
	case <-done:
		t.Fatal("the wait returned while the job was running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return after the job ended")
	}

	if runs.Load() != 1 {
		t.Fatalf("the job ran %d times", runs.Load())
	}

	if tails.isRunning(key) {
		t.Fatal("still running after it ended")
	}

	tails.forget(key)

	if _, ok := tails.backupOf(key); ok {
		t.Fatal("forgotten operation still known")
	}
}

// TestProbesAreServedWhileARestoreRunsWithoutTheManager: the liveness probe would get the process that
// is finishing a restore killed, so the probe address answers after the manager is gone.
func TestProbesAreServedWhileARestoreRunsWithoutTheManager(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := l.Addr().String()
	_ = l.Close()

	stop := serveProbes(addr)
	defer stop()

	var status int

	for range 50 {
		resp, err := http.Get("http://" + addr + "/healthz") //nolint:noctx // a test against a local port
		if err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()

			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if status != http.StatusOK {
		t.Fatalf("healthz = %d", status)
	}
}

func TestWaitForRestoreTailsReturnsAtOnceWithNothingRunning(t *testing.T) {
	start := time.Now()
	WaitForRestoreTails(time.Hour, "127.0.0.1:1")

	if time.Since(start) > 5*time.Second {
		t.Fatal("waited with no job running")
	}
}
