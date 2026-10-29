package uiapi

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// reviewingSubresources answers a SelfSubjectAccessReview with allowed only for the listed subresources.
func reviewingSubresources(c client.Client, allow ...string) client.Client {
	return interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if sar, ok := obj.(*authv1.SelfSubjectAccessReview); ok {
				a := sar.Spec.ResourceAttributes
				for _, s := range allow {
					if a.Resource == "ichorinstallations" && a.Verb == verbUpdate && a.Subresource == s {
						sar.Status.Allowed = true
					}
				}

				return nil
			}

			return c.Create(ctx, obj, opts...)
		},
	})
}

func settingsServer(t *testing.T, readOnly bool, allow ...string) (*Server, client.Client) {
	t.Helper()

	k8s := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(&ichorv1alpha1.IchorInstallation{
		Name: settings.SingletonName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: ichorOld},
	}).Build()

	return &Server{
		Client:   k8s,
		Settings: &settings.Reader{Client: k8s, ReadOnly: readOnly},
		Impersonate: func(Identity) (client.Client, error) {
			return reviewingSubresources(k8s, allow...), nil
		},
	}, k8s
}

func TestGetSettingsEditableFollowsRBAC(t *testing.T) {
	for _, tc := range []struct {
		allow             []string
		readOnly          bool
		reclaim, settings bool
	}{
		{[]string{subReclaim, subSettings}, false, true, true},
		{[]string{subSettings}, false, false, true},
		{nil, false, false, false},
		{[]string{subReclaim, subSettings}, true, false, false},
	} {
		s, _ := settingsServer(t, tc.readOnly, tc.allow...)

		resp, err := s.GetSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), connect.NewRequest(&pb.GetSettingsRequest{}))
		if err != nil {
			t.Fatal(err)
		}

		if m := resp.Msg; m.CanEditReclaim != tc.reclaim || m.CanEditSettings != tc.settings || m.ReadOnly != tc.readOnly || m.Version != ichorOld {
			t.Errorf("%+v: got %+v", tc, m)
		}
	}
}

func TestUpdateSettingsWritesOnlyChangedFields(t *testing.T) {
	s, k8s := settingsServer(t, false, subReclaim, subSettings)
	policy := "autoCorrect"

	if _, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), connect.NewRequest(&pb.UpdateSettingsRequest{
		Changes: &pb.SettingsFields{DriftPolicy: &policy},
	})); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := k8s.Get(context.Background(), settingsKey(), &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.Defaults.DriftPolicy != policy || got.Spec.Reclaim != nil || got.Spec.Users != nil || got.Spec.Version != "v0.1.0" {
		t.Fatalf("spec %+v", got.Spec)
	}
}

func TestUpdateSettingsReclaimNeedsConfirmationAndSetsAnnotation(t *testing.T) {
	s, k8s := settingsServer(t, false, subReclaim)
	on := true
	req := func(confirm bool) *connect.Request[pb.UpdateSettingsRequest] {
		return connect.NewRequest(&pb.UpdateSettingsRequest{Changes: &pb.SettingsFields{ReclaimEnabled: &on}, ConfirmReclaim: confirm})
	}

	if _, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), req(false)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unconfirmed: %v", err)
	}

	if _, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), req(true)); err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := k8s.Get(context.Background(), settingsKey(), &got); err != nil {
		t.Fatal(err)
	}

	if !*got.Spec.Reclaim.Enabled || !strings.HasPrefix(got.Annotations[ichorv1alpha1.ConfirmReclaimAnnotation], ichorv1alpha1.ConfirmReclaimValue) {
		t.Fatalf("got %+v %v", got.Spec, got.Annotations)
	}
}

func TestUpdateSettingsEnableTwice(t *testing.T) {
	s, k8s := settingsServer(t, false, subReclaim)
	on, off := true, false
	ctx := ContextWithIdentity(context.Background(), Identity{User: userAlice})
	set := func(v *bool) string {
		if _, err := s.UpdateSettings(ctx, connect.NewRequest(&pb.UpdateSettingsRequest{Changes: &pb.SettingsFields{ReclaimEnabled: v}, ConfirmReclaim: *v})); err != nil {
			t.Fatal(err)
		}

		var got ichorv1alpha1.IchorInstallation
		if err := k8s.Get(ctx, settingsKey(), &got); err != nil {
			t.Fatal(err)
		}

		return got.Annotations[ichorv1alpha1.ConfirmReclaimAnnotation]
	}

	first := set(&on)
	if set(&off) != "" {
		t.Fatal("disabling must drop the confirmation")
	}

	if second := set(&on); second == first || !strings.HasPrefix(second, ichorv1alpha1.ConfirmReclaimValue) {
		t.Fatalf("a second enable must write a fresh confirmation: %q then %q", first, second)
	}
}

func TestUpdateSettingsRefusals(t *testing.T) {
	ttl := "30s"
	ok := "30m"

	// Without the settings verb, and out of bounds, and read-only: nothing is written.
	for name, tc := range map[string]struct {
		readOnly bool
		allow    []string
		val      string
		code     connect.Code
	}{
		"no verb":   {false, []string{subReclaim}, ok, connect.CodePermissionDenied},
		"bounds":    {false, []string{subSettings}, ttl, connect.CodeInvalidArgument},
		"read-only": {true, []string{subSettings}, ok, connect.CodeFailedPrecondition},
	} {
		s, k8s := settingsServer(t, tc.readOnly, tc.allow...)

		_, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), connect.NewRequest(&pb.UpdateSettingsRequest{
			Changes: &pb.SettingsFields{TokenTtl: &tc.val},
		}))
		if connect.CodeOf(err) != tc.code {
			t.Errorf("%s: %v", name, err)
		}

		var got ichorv1alpha1.IchorInstallation
		if err := k8s.Get(context.Background(), settingsKey(), &got); err != nil || got.Spec.Users != nil {
			t.Errorf("%s: wrote %+v %v", name, got.Spec, err)
		}
	}
}

const (
	subReclaim  = ichorv1alpha1.IchorSubresourceReclaim
	subSettings = ichorv1alpha1.IchorSubresourceSettings
)
