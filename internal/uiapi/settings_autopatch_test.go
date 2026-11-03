package uiapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func TestUpdateSettingsWritesAutoPatch(t *testing.T) {
	s, k8s := settingsServer(t, false, subSettings)
	on, comps, window := true, "talos,cilium", "02:00-05:00"

	resp, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), connect.NewRequest(&pb.UpdateSettingsRequest{
		Changes: &pb.SettingsFields{AutoPatchEnabled: &on, AutoPatchComponents: &comps, AutoPatchWindow: &window},
	}))
	if err != nil {
		t.Fatal(err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := k8s.Get(context.Background(), settingsKey(), &got); err != nil {
		t.Fatal(err)
	}

	a := got.Spec.AutoPatch
	if a == nil || a.Enabled == nil || !*a.Enabled || len(a.Components) != 2 || a.Window != window {
		t.Fatalf("spec %+v", a)
	}

	if f := resp.Msg.GetSettings().GetSpec(); f.GetAutoPatchEnabled() != true || f.GetAutoPatchComponents() != comps || f.GetAutoPatchWindow() != window {
		t.Fatalf("response %+v", f)
	}
}

func TestUpdateSettingsRefusesABadAutoPatch(t *testing.T) {
	s, _ := settingsServer(t, false, subSettings)

	for _, f := range []*pb.SettingsFields{
		{AutoPatchWindow: new("25:00-26:00")},
		{AutoPatchComponents: new("etcd")},
	} {
		_, err := s.UpdateSettings(ContextWithIdentity(context.Background(), Identity{User: userAlice}), connect.NewRequest(&pb.UpdateSettingsRequest{Changes: f}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%+v: want InvalidArgument, got %v", f, err)
		}
	}
}

// TestReadOnlyStillAllowsClearingAHalt: clearing a halt is an action, so --settings-read-only allows it, and nothing else.
func TestReadOnlyStillAllowsClearingAHalt(t *testing.T) {
	s, k8s := settingsServer(t, true, subSettings)
	ctx := ContextWithIdentity(context.Background(), Identity{User: userAlice})

	on := true
	if _, err := s.UpdateSettings(ctx, connect.NewRequest(&pb.UpdateSettingsRequest{Changes: &pb.SettingsFields{AutoPatchEnabled: &on}})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("read-only must refuse a setting: %v", err)
	}

	if _, err := s.UpdateSettings(ctx, connect.NewRequest(&pb.UpdateSettingsRequest{
		Changes: &pb.SettingsFields{AutoPatchEnabled: &on, AutoPatchAcknowledgedHalt: new("ns/op")},
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("read-only must refuse a setting sent with the acknowledgement: %v", err)
	}

	if _, err := s.UpdateSettings(ctx, connect.NewRequest(&pb.UpdateSettingsRequest{Changes: &pb.SettingsFields{AutoPatchAcknowledgedHalt: new("ns/op")}})); err != nil {
		t.Fatalf("read-only must allow clearing a halt: %v", err)
	}

	var got ichorv1alpha1.IchorInstallation
	if err := k8s.Get(context.Background(), settingsKey(), &got); err != nil {
		t.Fatal(err)
	}

	if a := got.Spec.AutoPatch; a == nil || a.AcknowledgedHalt != "ns/op" || a.Enabled != nil {
		t.Fatalf("spec %+v", a)
	}
}
