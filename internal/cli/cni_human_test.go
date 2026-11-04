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

	"google.golang.org/protobuf/proto"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Golden tests of the human output of `ichor cni`, `ichor config`, `ichor etcd backup` and
// `ichor restore --live` (docs/adr/0118), on a pipe: no colour codes.

const valuesDiffText = "  debug:\n+  enabled: true\n-  old: 1\n"

func TestCniValuesDiffHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	h, out := plainHuman()
	if err := renderValuesDiff(h, valuesDiffText); err != nil {
		t.Fatal(err)
	}

	if out.String() != valuesDiffText {
		t.Errorf("a pipe gets the diff as it is: %q", out.String())
	}

	h, out = plainHuman()
	if err := renderValuesDiff(h, ""); err != nil || out.String() != "✓ no change to the effective values\n" {
		t.Errorf("empty diff: %q, %v", out.String(), err)
	}
}

func TestCniValuesDiffColoursTheChangedLinesOnATerminalOnly(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	h, out := ttyHuman()
	if err := renderValuesDiff(h, valuesDiffText); err != nil {
		t.Fatal(err)
	}

	want := "  debug:\n\x1b[32m+  enabled: true\x1b[0m\n\x1b[31m-  old: 1\x1b[0m\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestCniValuesSetAndDiffPrintHumanOrMachineOutput(t *testing.T) {
	c, _ := cniTestClient(t, true)

	outputFormat = outputHuman

	var out, errw bytes.Buffer
	if err := runCniValuesSet(context.Background(), c, strings.NewReader(""), &out, &errw, "", cniTestValues); err != nil {
		t.Fatal(err)
	}

	if want := "✓ updated the Cilium values of " + opTestCluster + " · " + testNamespace + "\n"; out.String() != want {
		t.Errorf("set: %q, want %q", out.String(), want)
	}

	out.Reset()

	if err := runCniValuesDiff(context.Background(), c, &out, "", cniTestValues); err != nil || out.String() != "✓ no change to the effective values\n" {
		t.Errorf("diff of the stored layer: %q, %v", out.String(), err)
	}

	// -o yaml: the wire message for set, the server's diff or the old sentence for diff.
	outputFormat = outputYAML

	out.Reset()

	if err := runCniValuesDiff(context.Background(), c, &out, "", cniTestValues); err != nil || out.String() != noValueChange+"\n" {
		t.Errorf("-o yaml diff: %q, %v", out.String(), err)
	}

	out.Reset()

	if err := runCniValuesSet(context.Background(), c, strings.NewReader(""), &out, &errw, "", cniTestValues); err != nil || !strings.Contains(out.String(), "effectiveYaml") {
		t.Errorf("-o yaml set: %q, %v", out.String(), err)
	}
}

func TestCniUpgradeStartedHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer

	resp := &pb.StartCniUpgradeResponse{Operation: patchRef("cni-upgrade-x"), FromVersion: "1.19.3", ToVersion: testCiliumVersion}
	if err := renderCniUpgradeStarted(newHuman(&out), resp); err != nil {
		t.Fatal(err)
	}

	if want := "✓ started cni upgrade operation cni-upgrade-x · ichor-system (1.19.3 → 1.20.4)\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestEffectiveConfigHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	resp := &pb.GetEffectiveConfigResponse{Config: &pb.EffectiveConfig{
		Machine: patchRef(cpUUID),
		Values: []*pb.EffectiveConfigValue{
			{Path: "machine.network.hostname", Value: `"cp-1"`, Origin: &pb.ConfigOrigin{Layer: pb.ConfigLayer_CONFIG_LAYER_SYSTEM, Source: "system"}},
			{Path: "machine.sysctls", Value: `{"net.core.somaxconn":"1024"}`, Origin: &pb.ConfigOrigin{Layer: pb.ConfigLayer_CONFIG_LAYER_ROLE, Source: hpModulesName}},
			{Path: "machine.token", Value: `"***"`, Secret: true, Origin: &pb.ConfigOrigin{Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER}},
		},
	}}

	var out bytes.Buffer
	if err := renderEffectiveConfig(newHuman(&out), resp); err != nil {
		t.Fatal(err)
	}

	want := "fa70962a-4fd9-4d01-88c5-f9966adcda99 · ichor-system\n" +
		"PATH                      VALUE                          LAYER\n" +
		"machine.network.hostname  \"cp-1\"                         system\n" +
		"machine.sysctls           {\"net.core.somaxconn\":\"1024\"}  role/cp-modules\n" +
		"machine.token             \"***\"                          cluster\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()

	if err := renderEffectiveConfig(newHuman(&out), &pb.GetEffectiveConfigResponse{Config: &pb.EffectiveConfig{Machine: patchRef(cpUUID)}}); err != nil ||
		!strings.HasSuffix(out.String(), "\n  no values\n") {
		t.Errorf("empty config: %q, %v", out.String(), err)
	}
}

// assertWireOutput checks that printAPI prints msg as writeMessage does, in both machine formats,
// whatever the human render would have printed.
func assertWireOutput[T proto.Message](t *testing.T, name string, msg T, render func(*human, T) error) {
	t.Helper()

	for _, format := range []string{outputYAML, outputJSON} {
		setHumanTestFlags(t, format)

		var got, want bytes.Buffer
		if err := printAPI(&got, msg, render); err != nil {
			t.Fatal(err)
		}

		if err := writeMessage(&want, format, msg); err != nil {
			t.Fatal(err)
		}

		if got.String() != want.String() || got.Len() == 0 || strings.Contains(got.String(), "✓") {
			t.Errorf("%s -o %s:\n%s\nwant:\n%s", name, format, got.String(), want.String())
		}
	}
}

func TestMachineFormatsOfTheseCommandsAreTheWireMessage(t *testing.T) {
	assertWireOutput(t, "config effective", &pb.GetEffectiveConfigResponse{Config: &pb.EffectiveConfig{
		Machine: patchRef(cpUUID), Values: []*pb.EffectiveConfigValue{{Path: "machine.type", Value: `"controlplane"`}},
	}}, renderEffectiveConfig)
	assertWireOutput(t, "cni upgrade", &pb.StartCniUpgradeResponse{Operation: patchRef("op"), FromVersion: "1", ToVersion: "2"}, renderCniUpgradeStarted)
	assertWireOutput(t, "restore --live", &pb.CreateOperationResponse{Operation: &pb.Operation{Ref: patchRef("etcd-restore-x")}}, renderRestoreStarted)
}

func TestRestoreLiveStartedHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer

	resp := &pb.CreateOperationResponse{Operation: &pb.Operation{Ref: patchRef("etcd-restore-x")}}
	if err := renderRestoreStarted(newHuman(&out), resp); err != nil {
		t.Fatal(err)
	}

	want := "✓ started etcd restore operation etcd-restore-x · ichor-system\n" +
		"  follow it with: ichor operation get etcd-restore-x --watch\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}
