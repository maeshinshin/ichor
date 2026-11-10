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
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	"github.com/maeshinshin/ichor/pkg/render"
)

// newcomerWithSANDrift gives the cluster a spec.certSANs and makes addr answer with a configuration
// rendered without it: a SAN the node lacks, which a patch cannot take away (class C).
func (e *flowEnv) newcomerWithSANDrift(t *testing.T, addr string, bundle *secrets.Bundle) {
	t.Helper()

	other := e.cluster.DeepCopy()

	e.cluster.Spec.CertSANs = []string{"drift.example.test"}
	if err := e.k8s.Update(context.Background(), e.cluster); err != nil {
		t.Fatal(err)
	}

	*e.writes = nil // the set-up is not a write of adopt

	m := newTestMachine(e.cluster.Name)
	m.Name, m.Spec.Role, m.Spec.Endpoint = "uuid-"+addr, testRoleWorker, addr

	cfg, _, err := render.Render(other, m, bundle, nil, nil)
	if err != nil {
		t.Fatalf("rendering the live configuration of %s: %v", addr, err)
	}

	e.live[addr] = cfg
}

// A machine blocked by configuration adopt cannot represent is not "nothing to adopt, every machine
// already matches a fresh render": the plan says it is blocked and the command refuses, writing nothing
// (hardware finding E1).
func TestAdoptAddMachineBlockedMachineIsNotNothingToAdopt(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomerWithSANDrift(t, addAddr, newTestBundle(t))

	d := env.addDeps(t)
	d.PlanOnly = true

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("want a refusal that says the machine is blocked, got %v\n%s", err, env.stderr.String())
	}

	out := env.stderr.String()
	if strings.Contains(out, "already matches a fresh render") {
		t.Fatalf("a blocked machine was reported as matching a fresh render:\n%s", out)
	}

	if !strings.Contains(out, "spec.certSANs") {
		t.Fatalf("the hint is missing:\n%s", out)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("writes: %v", *env.writes)
	}
}

// The plan names each machine's role and install disk, which the release writes into the
// TalosMachine, so that a wrong guess is seen before consent.
func TestAdoptAddMachinePlanPrintsRoleAndInstallDisk(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t))

	d := env.addDeps(t)
	d.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	out := env.stderr.String()
	for _, want := range []string{`machine "uuid-` + addAddr + `"`, "role worker", "install disk"} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan lacks %q:\n%s", want, out)
		}
	}
}

// The registry hint is true whichever side lacks the mirror: it no longer tells the user to copy the
// node's value into spec.registries when the node has no mirror at all.
func TestMirrorHintCoversANodeWithoutTheClustersMirror(t *testing.T) {
	got := strings.Join(explainUnrepresentable([]string{"RegistryMirrorConfig.endpoints", "RegistryMirrorConfig.name"}), "\n")

	for _, want := range []string{"node's own configuration", "cluster's mirrors", "spec.registries"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "to the node's value") {
		t.Errorf("the hint still tells to copy the node's value:\n%s", got)
	}
}
