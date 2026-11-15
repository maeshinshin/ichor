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
	"strings"
	"testing"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// kubePrismReaderFake is a node whose KubePrism status is whatever the test sets.
type kubePrismReaderFake struct {
	versionedReader
	host    string
	healthy bool
}

func (k *kubePrismReaderFake) KubePrismStatus(context.Context) (string, bool, error) {
	return k.host, k.healthy, nil
}

// takeoverHealthEnv is a takeover whose preflight runs only the KubePrism probe adopt hands it, so
// what the test varies is the node's answer. status maps a node address to its KubePrism state.
func takeoverHealthEnv(t *testing.T, status map[string]*kubePrismReaderFake) (*flowEnv, adoptDeps) {
	t.Helper()

	env, deps := ciliumFlowEnv(t, nil)
	deps.Preflight = func(ctx context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
		rep := &cilium.PreflightReport{}
		if in.KubePrism != nil {
			rep.KubePrismErr = in.KubePrism(ctx)
		}

		return rep, nil
	}
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		r := status[endpoint]
		r.current, r.version = env.live[endpoint], testTalosVersion

		return r, nil
	}

	return env, deps
}

func answering() *kubePrismReaderFake {
	return &kubePrismReaderFake{host: "127.0.0.1:7445", healthy: true}
}

// ADR 0103 precondition 5: KubePrism must answer on every node before anything is written.
func TestAdoptTakeoverRefusesWhenKubePrismDoesNotAnswerOnANode(t *testing.T) {
	for name, worker := range map[string]*kubePrismReaderFake{
		"up but unhealthy": {host: "127.0.0.1:7445"},
		"not running":      {},
		"on another port":  {host: "127.0.0.1:7446", healthy: true},
	} {
		t.Run(name, func(t *testing.T) {
			env, deps := takeoverHealthEnv(t, map[string]*kubePrismReaderFake{flowCPAddr: answering(), flowWKAddr: worker})

			err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
			if err == nil || !strings.Contains(err.Error(), "node "+flowWorker) || !strings.Contains(err.Error(), "KubePrism") ||
				!strings.Contains(err.Error(), "nothing was written") {
				t.Fatalf("runAdopt = %v, want a refusal naming KubePrism and node %s", err, flowWorker)
			}

			// The command to run is paste-ready: the failing node's address only, not the healthy one's.
			if want := "talosctl -n " + flowWKAddr + " get kubeprismstatuses"; !strings.Contains(err.Error(), want) {
				t.Fatalf("runAdopt = %v, want the command %q", err, want)
			}

			if len(*env.writes) != 0 {
				t.Fatalf("wrote: %v", *env.writes)
			}
		})
	}
}

// With KubePrism answering everywhere the same plan goes through.
func TestAdoptTakeoverPlanPassesWhenKubePrismAnswersEverywhere(t *testing.T) {
	env, deps := takeoverHealthEnv(t, map[string]*kubePrismReaderFake{flowCPAddr: answering(), flowWKAddr: answering()})
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatal(err)
	}
}

// A node whose Talos client cannot say is not taken for healthy.
func TestAdoptTakeoverRefusesWhenKubePrismCannotBeAsked(t *testing.T) {
	env, deps := ciliumFlowEnv(t, nil)
	deps.Preflight = func(ctx context.Context, in cilium.PreflightInput) (*cilium.PreflightReport, error) {
		rep := &cilium.PreflightReport{}
		if in.KubePrism != nil {
			rep.KubePrismErr = in.KubePrism(ctx)
		}

		return rep, nil
	}
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &versionedReader{current: env.live[endpoint], version: testTalosVersion}, nil
	}

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), "could not ask") ||
		!strings.Contains(err.Error(), "talosctl -n "+flowCPAddr+","+flowWKAddr+" get kubeprismstatuses") {
		t.Fatalf("runAdopt = %v, want a refusal that says KubePrism could not be asked", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("wrote: %v", *env.writes)
	}
}

// ADR 0103 precondition 5: an agent or the operator that is not Ready refuses with nothing written.
func TestAdoptTakeoverRefusesWhenAgentsAreNotReady(t *testing.T) {
	env, deps := ciliumFlowEnv(t, func(context.Context, cilium.PreflightInput) (*cilium.PreflightReport, error) {
		return &cilium.PreflightReport{AgentsErr: errors.New("cilium: the DaemonSet cilium is not Ready on all nodes (2 of 3 agents Ready; not Ready: pod cilium-x on node w2)")}, nil
	})

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), "node w2") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("runAdopt = %v, want the refusal naming node w2", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("wrote: %v", *env.writes)
	}
}
