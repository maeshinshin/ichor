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
)

func runInitForOutput(t *testing.T, output string) string {
	t.Helper()
	withTempHome(t)

	deps, _, _ := newFakeInitDeps(t, fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build())

	var stdout bytes.Buffer

	deps.Stdout = &stdout
	deps.Output = output

	if err := runInit(context.Background(), validInitOptions(), deps); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	return stdout.String()
}

func TestRunInit_DefaultOutputIsOneLinePerCreatedObject(t *testing.T) {
	out := runInitForOutput(t, "")

	for _, want := range []string{
		"init: created TalosCluster " + testNamespace + "/" + testInitClusterName,
		"init: created TalosMachine " + testNamespace + "/" + testInitMachineUUID,
		"init: created TalosConfigPatch " + testNamespace + "/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}

	for _, doc := range []string{"apiVersion:", "kind:", "---", "spec:"} {
		if strings.Contains(out, doc) {
			t.Errorf("default stdout has YAML (%q):\n%s", doc, out)
		}
	}
}

func TestRunInit_ExplicitOutputYAMLPrintsTheObjects(t *testing.T) {
	out := runInitForOutput(t, outputYAML)

	if !strings.Contains(out, "spec:") || !strings.Contains(out, "---") {
		t.Errorf("-o yaml should print the objects:\n%.300s", out)
	}
}
