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
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/internal/installmanifest"
)

func runInitForOutput(t *testing.T, output string) (stdout, stderr string) {
	t.Helper()
	withTempHome(t)

	deps, _, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	var out, errw bytes.Buffer

	deps.Stdout, deps.Stderr = &out, &errw
	deps.Output = output

	if err := runInit(context.Background(), validInitOptions(), deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	return out.String(), errw.String()
}

func TestRunInit_DefaultOutputIsOneLinePerCreatedObject(t *testing.T) {
	out, progress := runInitForOutput(t, "")

	// The created objects are progress, on stderr; stdout is for -o only.
	for _, want := range []string{
		"created TalosCluster " + testNamespace + "/" + testInitClusterName,
		"created TalosMachine " + testNamespace + "/" + testInitMachineUUID,
		"created TalosConfigPatch " + testNamespace + "/",
	} {
		if !strings.Contains(progress, want) {
			t.Errorf("stderr lacks %q:\n%s", want, progress)
		}
	}

	if out != "" {
		t.Errorf("default stdout is not empty:\n%s", out)
	}

	for _, doc := range []string{"apiVersion:", "kind:", "---", "spec:"} {
		if strings.Contains(out, doc) {
			t.Errorf("default stdout has YAML (%q):\n%s", doc, out)
		}
	}
}

func TestRunInit_ExplicitOutputYAMLPrintsTheObjects(t *testing.T) {
	out, progress := runInitForOutput(t, outputYAML)

	if !strings.Contains(out, "spec:") || !strings.Contains(out, "---") {
		t.Errorf("-o yaml should print the objects:\n%.300s", out)
	}

	if strings.Contains(out, "✓") || strings.Contains(out, "Ichor init") {
		t.Errorf("-o yaml stdout has progress lines:\n%.300s", out)
	}

	if !strings.Contains(progress, "✓ Cluster resources") {
		t.Errorf("stderr lacks the step lines:\n%s", progress)
	}
}

// The steps of a whole run, in order, as a pipe or a CI log shows them: no escape sequences, the
// secrets-bundle reminder on its line.
func TestRunInit_StepsOnAPlainWriter(t *testing.T) {
	_, progress := runInitForOutput(t, "")

	t.Logf("\n%s", progress)

	if strings.Contains(progress, "\x1b") || strings.Contains(progress, "\r") {
		t.Errorf("escape sequences on a writer that is no terminal: %q", progress)
	}

	want := []string{
		"Ichor init · " + testInitClusterName,
		"✓ Secrets bundle — ",
		"← back this up now",
		"✓ Configure node — ",
		"✓ Bootstrap etcd — kubeconfig saved to ",
		"✓ Kubernetes API — reachable",
		"✓ Install Ichor — ",
		"✓ Cluster resources — TalosCluster, TalosMachine, TalosConfigPatch",
		"✓ ichor-ui — ready",
		"Done in ",
		"! ichor-ui has no sign-in (--allow-unauthenticated)",
		"ichor cluster status " + testInitClusterName,
	}

	rest := progress
	for _, w := range want {
		i := strings.Index(rest, w)
		if i < 0 {
			t.Fatalf("%q is missing, or out of order, in:\n%s", w, progress)
		}

		rest = rest[i+len(w):]
	}
}

// A failed step is a ✗ line with the error below it, and the run prints no total.
func TestRunInit_FailedStepShowsTheErrorBelowIt(t *testing.T) {
	withTempHome(t)

	deps, _, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	var stderr bytes.Buffer

	deps.Stderr = &stderr
	deps.IchorManifest = []byte(installmanifest.PlaceholderSentinel)

	err := runInit(context.Background(), validInitOptions(), deps)
	if err == nil {
		t.Fatal("a placeholder install manifest should fail init")
	}

	out := stderr.String()
	if !strings.Contains(out, "✗ Install Ichor — failed") || !strings.Contains(out, "\n    "+err.Error()) {
		t.Errorf("the failed step and its error are not shown:\n%s", out)
	}

	if strings.Contains(out, "Done in") || strings.Contains(out, "Next") {
		t.Errorf("a failed run prints a total or next steps:\n%s", out)
	}
}
