package v1alpha1

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// sarByUser answers SubjectAccessReviews from a table user -> allowed ichorinstallations subresources.
const (
	gateAdmin     = "admin"
	gateVersioner = "versioner"
	gateSettings  = "settings"
	// needVersionRight is what a refusal for a missing right on the version subresource says.
	needVersionRight = "ichorinstallations/version"
)

func sarByUser(t *testing.T, table map[string][]string) client.Client {
	t.Helper()

	return sarClient(t, func(sar *authzv1.SubjectAccessReview) bool {
		ra := sar.Spec.ResourceAttributes

		return ra.Resource == "ichorinstallations" && ra.Verb == "update" && slices.Contains(table[sar.Spec.User], ra.Subresource)
	})
}

func settingsCtx(user string, dryRun bool) context.Context {
	var req admission.Request

	req.UserInfo, req.DryRun = authnv1.UserInfo{Username: user}, &dryRun

	return admission.NewContextWithRequest(context.Background(), req)
}

func inst(mod func(*ichorv1alpha1.IchorInstallationSpec)) *ichorv1alpha1.IchorInstallation {
	i := &ichorv1alpha1.IchorInstallation{
		Name: settings.SingletonName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.1.0"},
	}
	if mod != nil {
		mod(&i.Spec)
	}

	return i
}

func TestSettingsGate(t *testing.T) {
	t.Parallel()

	yes := true
	enableReclaim := func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.Reclaim = &ichorv1alpha1.IchorReclaimSettings{Enabled: &yes}
	}
	ttl := func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.Users = &ichorv1alpha1.IchorUserSettings{TokenTTL: &metav1.Duration{Duration: 10 * time.Minute}}
	}
	autoOn := func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.AutoPatch = &ichorv1alpha1.IchorAutoPatchSettings{Enabled: &yes, Window: "02:00-05:00"}
	}
	autoAck := func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.AutoPatch = &ichorv1alpha1.IchorAutoPatchSettings{AcknowledgedHalt: "ns/op"}
	}
	bump := func(s *ichorv1alpha1.IchorInstallationSpec) { s.Version = "v0.2.0" }
	skipSig := func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: "skip", Reason: "demo build"}
	}
	pin := func(s *ichorv1alpha1.IchorInstallationSpec) { s.InstallerDigest = "sha256:" + strings.Repeat("a", 64) }
	table := map[string][]string{
		gateAdmin:     {"version", gateSettings},
		gateSettings:  {gateSettings},
		gateVersioner: {"version"},
		bgUnitPlain:   nil,
	}

	tests := []struct {
		name     string
		user     string
		old, new *ichorv1alpha1.IchorInstallation
		readOnly bool
		dryRun   bool
		wantErr  string // substring; empty means allowed
		wantEv   string // substring of the Event; empty means none expected
	}{
		{name: "no spec change needs nothing", user: bgUnitPlain, old: inst(nil), new: inst(nil)},
		{name: "version needs /version", user: bgUnitPlain, old: inst(nil), new: inst(bump), wantErr: needVersionRight},
		{name: "version by holder", user: gateVersioner, old: inst(nil), new: inst(bump), wantEv: "spec.version: v0.1.0 -> v0.2.0"},
		{name: "skipping the signature check needs /version", user: gateSettings, old: inst(nil), new: inst(skipSig), wantErr: needVersionRight},
		{name: "skipping the signature check is recorded with its reason", user: gateVersioner, old: inst(nil), new: inst(skipSig), wantEv: "spec.installerSignature.mode: <unset> -> skip"},
		{name: "a digest pin needs /version", user: gateSettings, old: inst(nil), new: inst(pin), wantErr: needVersionRight},
		{name: "a digest pin by holder", user: gateVersioner, old: inst(nil), new: inst(pin), wantEv: "spec.installerDigest: <unset> -> sha256:"},
		{name: "users need /settings", user: gateVersioner, old: inst(nil), new: inst(ttl), wantErr: "ichorinstallations/settings"},
		{name: "users by holder", user: gateSettings, old: inst(nil), new: inst(ttl), wantEv: "spec.users.tokenTTL: <unset> -> 10m0s"},
		{name: "the deprecated reclaim field is ignored: no check, no Event", user: bgUnitPlain, old: inst(nil), new: inst(enableReclaim)},
		{name: "read-only denies users", user: gateAdmin, old: inst(nil), new: inst(ttl), readOnly: true, wantErr: "managed by the deployment"},
		{name: "read-only still allows a version bump", user: gateVersioner, old: inst(nil), new: inst(bump), readOnly: true, wantEv: "spec.version"},
		{name: "auto-patch needs /settings", user: gateVersioner, old: inst(nil), new: inst(autoOn), wantErr: "the \"update\" verb"},
		{name: "auto-patch by holder", user: gateSettings, old: inst(nil), new: inst(autoOn), wantEv: "spec.autoPatch.enabled: <unset> -> true"},
		{name: "read-only denies auto-patch", user: gateAdmin, old: inst(nil), new: inst(autoOn), readOnly: true, wantErr: "managed by the deployment"},
		{name: "read-only still allows clearing a halt", user: gateSettings, old: inst(nil), new: inst(autoAck), readOnly: true, wantEv: "spec.autoPatch.acknowledgedHalt"},
		{name: "clearing a halt still needs /settings", user: gateVersioner, old: inst(nil), new: inst(autoAck), readOnly: true, wantErr: "the \"update\" verb"},
		{name: "the controller is exempt", user: testControllerUser, old: nil, new: inst(bump)},
		{name: "dry run emits no Event", user: gateVersioner, old: inst(nil), new: inst(bump), dryRun: true},
		{name: "create is checked as a change from empty", user: bgUnitPlain, old: nil, new: inst(nil), wantErr: needVersionRight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := record.NewFakeRecorder(10)
			g := &SettingsGate{
				Client: sarByUser(t, table), ReadOnly: tt.readOnly, Recorder: rec,
				ExemptUsers: []string{testControllerUser},
			}

			err := g.check(settingsCtx(tt.user, tt.dryRun), tt.old, tt.new)

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("denied: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}

			var ev string
			select {
			case ev = <-rec.Events:
			default:
			}

			if tt.wantErr != "" && ev != "" {
				t.Fatalf("an Event was emitted for a denied write: %s", ev)
			}

			if tt.wantEv != "" && (!strings.Contains(ev, tt.wantEv) || !strings.Contains(ev, tt.user)) {
				t.Fatalf("event %q must name the user %q and contain %q", ev, tt.user, tt.wantEv)
			}

			if tt.wantEv == "" && tt.wantErr == "" && ev != "" {
				t.Fatalf("unexpected Event %q", ev)
			}
		})
	}
}

func TestDeprecatedReclaimWarns(t *testing.T) {
	t.Parallel()

	if w := deprecatedReclaim(inst(nil)); len(w) != 0 {
		t.Fatalf("no warning expected without the field: %v", w)
	}

	on := true
	w := deprecatedReclaim(inst(func(s *ichorv1alpha1.IchorInstallationSpec) {
		s.Reclaim = &ichorv1alpha1.IchorReclaimSettings{Enabled: &on}
	}))

	if len(w) != 1 || !strings.Contains(w[0], "RBAC") {
		t.Fatalf("want one warning pointing: %v", w)
	}
}
