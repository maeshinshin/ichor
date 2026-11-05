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
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	cniTestEnabled = "enabled"
	cniTestStored  = `{"debug":{"enabled":true}}`
)

const cniTestValues = "debug:\n  enabled: true\n"

func cniTestClient(t *testing.T, yes bool) (*apiClients, client.Client) {
	t.Helper()

	setAPITestFlags(t, yes)

	cl := newTestCluster()
	cl.Spec.CNI.KubeProxy = cniTestEnabled

	return freshClients(t, []client.Object{cl})
}

func cniStoredValues(t *testing.T, k8s client.Client) string {
	t.Helper()

	var c ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: opTestCluster}, &c); err != nil {
		t.Fatal(err)
	}

	if c.Spec.CNI.Cilium == nil || c.Spec.CNI.Cilium.Values == nil {
		return ""
	}

	return string(c.Spec.CNI.Cilium.Values.Raw)
}

func TestCniValuesSetValidatesShowsTheDiffAndWrites(t *testing.T) {
	c, k8s := cniTestClient(t, true)

	var out, errw bytes.Buffer
	if err := runCniValuesSet(context.Background(), c, strings.NewReader(""), &out, &errw, "", cniTestValues); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "+  enabled: true") {
		t.Fatalf("the diff must be shown: %q", errw.String())
	}

	if got := cniStoredValues(t, k8s); got != cniTestStored {
		t.Fatalf("stored values = %q", got)
	}

	var got bytes.Buffer
	if err := runCniValuesGet(context.Background(), c, &got, "", cniLayerUser); err != nil || !strings.Contains(got.String(), "enabled: true") {
		t.Fatalf("get: %q, %v", got.String(), err)
	}
}

func TestCniValuesSetPrintsEveryProblemAndWritesNothing(t *testing.T) {
	c, k8s := cniTestClient(t, true)

	var errw bytes.Buffer

	err := runCniValuesSet(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw, "", "ipam:\n  mode: x\nk8sServicePort: 1\n")
	if err == nil || !strings.Contains(err.Error(), "2 problem(s)") {
		t.Fatalf("want 2 problems, got %v", err)
	}

	for _, want := range []string{"ipam.mode", "k8sServicePort"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, errw.String())
		}
	}

	if cniStoredValues(t, k8s) != "" {
		t.Fatal("invalid values were written")
	}
}

func TestCniValuesSetDeclinedWritesNothing(t *testing.T) {
	c, k8s := cniTestClient(t, false)

	err := runCniValuesSet(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, "", cniTestValues)
	if err == nil || !strings.Contains(err.Error(), "aborted") || cniStoredValues(t, k8s) != "" {
		t.Fatalf("want aborted and nothing written, got %v / %q", err, cniStoredValues(t, k8s))
	}
}

func TestCniValuesDiffShowsTheChangeAndWritesNothing(t *testing.T) {
	c, k8s := cniTestClient(t, true)

	var out bytes.Buffer
	if err := runCniValuesDiff(context.Background(), c, &out, "", cniTestValues); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "+  enabled: true") || cniStoredValues(t, k8s) != "" {
		t.Fatalf("diff = %q, stored = %q", out.String(), cniStoredValues(t, k8s))
	}
}

func TestCniValuesGetOwnedListsTheReasons(t *testing.T) {
	c, _ := cniTestClient(t, true)

	var out bytes.Buffer
	if err := runCniValuesGet(context.Background(), c, &out, "", cniLayerOwned); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "ipam.mode: kubernetes  # ") {
		t.Fatalf("owned = %q", out.String())
	}

	if err := runCniValuesGet(context.Background(), c, &out, "", "nope"); err == nil {
		t.Fatal("an unknown layer must be an error")
	}
}

func TestCniValuesEditSavesWhatTheEditorLeavesAndSkipsAnUnchangedFile(t *testing.T) {
	c, k8s := cniTestClient(t, true)

	var errw bytes.Buffer

	untouched := func(string) error { return nil }
	if err := runCniValuesEdit(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw, "", untouched); err != nil ||
		!strings.Contains(errw.String(), "No changes") || cniStoredValues(t, k8s) != "" {
		t.Fatalf("unchanged edit: %v %q", err, errw.String())
	}

	write := func(path string) error { return os.WriteFile(path, []byte(cniTestValues), 0o600) }
	if err := runCniValuesEdit(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "", write); err != nil {
		t.Fatal(err)
	}

	if cniStoredValues(t, k8s) != cniTestStored {
		t.Fatalf("stored = %q", cniStoredValues(t, k8s))
	}

	bad := func(path string) error { return os.WriteFile(path, []byte("ipam:\n  mode: x\n"), 0o600) }
	if err := runCniValuesEdit(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "", bad); err == nil ||
		cniStoredValues(t, k8s) != cniTestStored {
		t.Fatalf("an invalid edit must fail and leave the stored values: %v", err)
	}
}

func TestCniUpgradeRefusedWhenNoUpgradeIsOffered(t *testing.T) {
	c, _ := cniTestClient(t, true)

	err := runCniUpgrade(context.Background(), c, &bytes.Buffer{}, "")
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}
