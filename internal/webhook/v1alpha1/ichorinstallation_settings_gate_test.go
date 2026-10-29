package v1alpha1

import (
	"context"
	"strings"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// sarByUser answers SubjectAccessReviews from a table user -> allowed ichorinstallations subresources.
const (
	gateAdmin     = "admin"
	gateVersioner = "versioner"
	gateSettings  = "settings"
)

func sarByUser(t *testing.T, table map[string][]string) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			sar, ok := obj.(*authzv1.SubjectAccessReview)
			if !ok {
				return cl.Create(ctx, obj, opts...)
			}

			ra := sar.Spec.ResourceAttributes
			for _, sub := range table[sar.Spec.User] {
				if ra.Resource == "ichorinstallations" && ra.Verb == "update" && ra.Subresource == sub {
					sar.Status.Allowed = true
				}
			}

			return nil
		},
	}).Build()
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
	bump := func(s *ichorv1alpha1.IchorInstallationSpec) { s.Version = "v0.2.0" }
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
		{name: "version needs /version", user: bgUnitPlain, old: inst(nil), new: inst(bump), wantErr: "ichorinstallations/version"},
		{name: "version by holder", user: gateVersioner, old: inst(nil), new: inst(bump), wantEv: "spec.version: v0.1.0 -> v0.2.0"},
		{name: "users need /settings", user: gateVersioner, old: inst(nil), new: inst(ttl), wantErr: "ichorinstallations/settings"},
		{name: "users by holder", user: gateSettings, old: inst(nil), new: inst(ttl), wantEv: "spec.users.tokenTTL: <unset> -> 10m0s"},
		{name: "the deprecated reclaim field is ignored: no check, no Event", user: bgUnitPlain, old: inst(nil), new: inst(enableReclaim)},
		{name: "read-only denies users", user: gateAdmin, old: inst(nil), new: inst(ttl), readOnly: true, wantErr: "managed by the deployment"},
		{name: "read-only still allows a version bump", user: gateVersioner, old: inst(nil), new: inst(bump), readOnly: true, wantEv: "spec.version"},
		{name: "the controller is exempt", user: testControllerUser, old: nil, new: inst(bump)},
		{name: "dry run emits no Event", user: gateVersioner, old: inst(nil), new: inst(bump), dryRun: true},
		{name: "create is checked as a change from empty", user: bgUnitPlain, old: nil, new: inst(nil), wantErr: "ichorinstallations/version"},
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
