package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestSettingsUpdateRequest(t *testing.T) {
	_, err := settingsUpdateRequest([]string{"reclaim.enabled=true"})
	if err == nil || !strings.Contains(err.Error(), "RBAC") {
		t.Fatalf("reclaim.enabled must be refused with a pointer: %v", err)
	}

	req, err := settingsUpdateRequest([]string{"users.tokenTTL=15m"})
	if err != nil {
		t.Fatal(err)
	}

	if *req.Changes.TokenTtl != "15m0s" || req.Changes.ReclaimEnabled != nil {
		t.Fatalf("%v", req)
	}

	if req.Changes.RefreshLifetime != nil || req.Changes.DriftPolicy != nil {
		t.Fatal("unnamed keys must stay unchanged")
	}

	for _, bad := range []string{"users.tokenTTL=2h", "nope=1", "defaults.driftPolicy=enforce"} {
		if _, err := settingsUpdateRequest([]string{bad}); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestSettingsTable(t *testing.T) {
	ttl := "30m"
	var sb strings.Builder

	err := writeSettingsTable(&sb, &pb.GetSettingsResponse{
		Version: testSettingsVersion, Manager: "helm", ReadOnly: true,
		Spec:      &pb.SettingsFields{TokenTtl: &ttl},
		Effective: &pb.SettingsEffective{TokenTtl: &pb.SettingValue{Value: "30m", Source: "spec"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := sb.String()
	for _, want := range []string{"users.tokenTTL  30m  spec", "defaults.driftPolicy  -", "managed by the deployment"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

const (
	testSettingsVersion = "v0.1.0"
	settingsTTL         = "30m"
)

func TestSettingsMachineOutputIsUnchanged(t *testing.T) {
	r := &pb.GetSettingsResponse{Version: testSettingsVersion, Manager: string(provenanceHelm)}

	for _, format := range []string{outputYAML, outputJSON} {
		setHumanTestFlags(t, format)

		var got, want bytes.Buffer

		cmd := &cobra.Command{}
		cmd.SetOut(&got)

		if err := writeSettings(cmd, r); err != nil {
			t.Fatal(err)
		}

		if err := writeMessage(&want, format, r); err != nil {
			t.Fatal(err)
		}

		if got.String() != want.String() || strings.Contains(got.String(), "SETTING") {
			t.Errorf("-o %s:\n%s\nwant:\n%s", format, got.String(), want.String())
		}
	}
}

func TestSettingsTableHumanOutput(t *testing.T) {
	ttl := settingsTTL

	var sb strings.Builder

	err := writeSettingsTable(&sb, &pb.GetSettingsResponse{
		Version: testSettingsVersion, Manager: string(provenanceHelm), ReadOnly: true,
		Spec:      &pb.SettingsFields{TokenTtl: &ttl},
		Effective: &pb.SettingsEffective{TokenTtl: &pb.SettingValue{Value: settingsTTL, Source: sourceSpec}},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := "SETTING                VALUE   SOURCE\n" +
		"version                v0.1.0  spec\n" +
		"installedVersion       -       status\n" +
		"deployment.manager     helm    spec\n" +
		"users.tokenTTL         30m     spec\n" +
		"users.refreshLifetime  -       -\n" +
		"defaults.driftPolicy   -       -\n" +
		"updateCheck.enabled    -       -\n" +
		"autoPatch.enabled      -       -\n" +
		"autoPatch.components   -       -\n" +
		"autoPatch.window       -       -\n" +
		"\n" +
		"! The settings are managed by the deployment (--settings-read-only); change them there.\n"
	if sb.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", sb.String(), want)
	}
}
