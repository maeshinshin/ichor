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
)

// These tests pin adopt's human output on a writer that is no terminal, so as a pipe or
// a CI log shows it, and that -o yaml keeps the "adopt:" lines.

// doneInPrefix starts the line that ends a run.
const doneInPrefix = "Done in "

var errDialRefused = errors.New("connection refused")

func humanAdoptDeps(t *testing.T, env *flowEnv) adoptDeps {
	t.Helper()

	deps := env.deps(t)
	deps.Output = outputHuman

	return deps
}

// The steps of a whole run, in order (a cluster with no CNI that Ichor has never seen: Cilium, Ichor,
// the resources, the hand-over, the release): no escape sequences, the findings of the plan as
// lines of their own, the advice to rotate the CAs after "Done in", the next commands last.
func TestRunAdopt_StepsOnAPlainWriter(t *testing.T) {
	f := newNoCNIFlow(t)
	f.deps.Output = outputHuman
	f.deps.SaveBundle = func() error { return nil }

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	out := f.env.stderr.String()

	t.Logf("\n%s", out)

	if strings.Contains(out, "\x1b") || strings.Contains(out, "\r") {
		t.Errorf("escape sequences on a writer that is no terminal: %q", out)
	}

	if f.env.stdout.Len() != 0 {
		t.Errorf("human output prints no patches on stdout:\n%s", f.env.stdout.String())
	}

	want := []string{
		"Ichor adopt · " + f.env.cluster.Name,
		"namespace " + testNamespace + " · discovering from " + flowCPAddr,
		"✓ Read cluster — Talos ",
		"2 read",
		"no TalosCluster yet",
		"no CNI found: Cilium will be installed by adopt",
		"✓ Inspect CNI",
		"✓ Plan — ",
		"✓ Secrets bundle — stored under ~/.ichor/clusters/" + f.env.cluster.Name + "/",
		"✓ Install Cilium",
		"✓ Create resources — ",
		"… Cilium hand-over — waiting for the controller",
		"✓ Cilium hand-over — Helm release owned by Ichor",
		"✓ Release holds — ",
		doneInPrefix,
		"! " + caRotationAdvice,
		"Next",
		"ichor cluster status " + f.env.cluster.Name + " -n " + testNamespace,
		"ichor machine list -n " + testNamespace,
	}

	assertInOrder(t, out, want)

	if strings.Contains(out, "adopt: ") {
		t.Errorf("a legacy line in the human output:\n%s", out)
	}
}

// --plan stops after the plan: the Ichor-owned settings stay ! lines, and no write step runs.
func TestRunAdopt_PlanOnlyInHumanOutput(t *testing.T) {
	env := newFlowEnv(t, cniCilium, unmanagedLive)
	deps := humanAdoptDeps(t, env)
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	out := env.stderr.String()

	t.Logf("\n%s", out)

	if len(*env.writes) != 0 {
		t.Fatalf("--plan wrote: %v", *env.writes)
	}

	for _, want := range []string{"plan only, nothing is written", "! machine \"" + flowCP + "\": Ichor-owned settings", "--plan: nothing was written", doneInPrefix} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	for _, unwanted := range []string{"Create resources", "Release holds", "Next"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a --plan run shows %q:\n%s", unwanted, out)
		}
	}
}

// A refusal inside a step is shown below the step, once, and the run has no total.
func TestRunAdopt_FailedStepShowsTheErrorBelowIt(t *testing.T) {
	env := newFlowEnv(t, cniCilium, unmanagedLive)
	deps := humanAdoptDeps(t, env)
	deps.DialTalos = func(context.Context, string) (talosConfigReader, error) { return nil, errDialRefused }

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !errorShown(err) {
		t.Fatalf("want an error a step already showed, got %v", err)
	}

	out := env.stderr.String()

	t.Logf("\n%s", out)

	if !strings.Contains(out, "✗ Read cluster — failed") || !strings.Contains(out, "\n    "+err.Error()) {
		t.Errorf("the failed step and its error are not shown:\n%s", out)
	}

	if strings.Contains(out, "Done in") || strings.Contains(out, "Next") {
		t.Errorf("a failed run prints a total or next steps:\n%s", out)
	}
}

// -o yaml keeps the lines adopt always printed, and the patches on stdout.
func TestRunAdopt_MachineFormatKeepsTheAdoptLines(t *testing.T) {
	env := newFlowEnv(t, cniCilium, unmanagedLive)
	deps := env.deps(t)
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	out := env.stderr.String()

	for _, want := range []string{"adopt: machine \"" + flowCP + "\": ", "adopt: --plan: nothing was written\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	for _, unwanted := range []string{"✓", "Done in", "Ichor adopt"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("-o yaml shows %q:\n%s", unwanted, out)
		}
	}
}

// The reminder about a recovered secrets bundle is one warning with the path and what to do.
func TestBundleNagWriterMakesOneWarning(t *testing.T) {
	var out strings.Builder

	p := newProgress(&out)

	nagBackup(bundleNagWriter{p}, "/some/dir/secrets.yaml")

	want := "! the cluster's secrets bundle was written to /some/dir/secrets.yaml. It is the cluster's only root of trust: " +
		"back it up now, somewhere other than this machine\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}
