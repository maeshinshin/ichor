package cli

import (
	"strings"
	"testing"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestSettingsUpdateRequest(t *testing.T) {
	if _, err := settingsUpdateRequest([]string{"reclaim.enabled=true"}, false); err == nil {
		t.Fatal("enabling reclaim without --confirm-reclaim must fail")
	}

	req, err := settingsUpdateRequest([]string{"reclaim.enabled=true", "users.tokenTTL=15m"}, true)
	if err != nil {
		t.Fatal(err)
	}

	if !*req.Changes.ReclaimEnabled || *req.Changes.TokenTtl != "15m0s" || !req.ConfirmReclaim {
		t.Fatalf("%v", req)
	}

	if req.Changes.RefreshLifetime != nil || req.Changes.DriftPolicy != nil {
		t.Fatal("unnamed keys must stay unchanged")
	}

	// Disabling needs no confirmation.
	if _, err := settingsUpdateRequest([]string{"reclaim.enabled=false"}, false); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"users.tokenTTL=2h", "nope=1", "defaults.driftPolicy=enforce"} {
		if _, err := settingsUpdateRequest([]string{bad}, true); err == nil {
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
		Effective: &pb.SettingsEffective{ReclaimEnabled: &pb.SettingValue{Value: testOff, Source: "deployment"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := sb.String()
	for _, want := range []string{"reclaim.enabled  " + testOff + "  deployment", "users.tokenTTL", "30m", "defaults.driftPolicy  -", "managed by the deployment"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

const (
	testSettingsVersion = "v0.1.0"
	testOff             = "false"
)
