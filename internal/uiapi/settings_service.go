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

package uiapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// SettingsService (ADR 0105, Part B): the runtime settings of the IchorInstallation, read and written as the caller.
// The settings webhook is the second line: it judges the same SubjectAccessReviews and the confirm annotation.

func settingsKey() types.NamespacedName { return types.NamespacedName{Name: settings.SingletonName} }

// mayEditSetting asks, as the caller, for update on ichorinstallations/<sub>.
func (s *Server) mayEditSetting(ctx context.Context, sub string) (bool, error) {
	return s.mayAll(ctx, authv1.ResourceAttributes{
		Verb: "update", Group: ichorv1alpha1.GroupVersion.Group, Resource: "ichorinstallations", Subresource: sub,
		Name: settings.SingletonName,
	})
}

func (s *Server) settingsReadOnly() bool { return s.Settings != nil && s.Settings.ReadOnly }

func settingValuePB[T any](v settings.Value[T], str func(T) string) *pb.SettingValue {
	return &pb.SettingValue{Value: str(v.V), Source: v.Source}
}

func effectivePB(e settings.Effective) *pb.SettingsEffective {
	return &pb.SettingsEffective{
		TokenTtl:        settingValuePB(e.TokenTTL, time.Duration.String),
		RefreshLifetime: settingValuePB(e.RefreshLifetime, time.Duration.String),
		DriftPolicy:     settingValuePB(e.DriftPolicy, func(s string) string { return s }),
	}
}

// statusEffectivePB maps status.effective; nil until the controller has written it.
func statusEffectivePB(e *ichorv1alpha1.IchorEffectiveSettings) *pb.SettingsEffective {
	if e == nil {
		return nil
	}

	v := func(x *ichorv1alpha1.IchorEffectiveValue) *pb.SettingValue {
		if x == nil {
			return nil
		}

		return &pb.SettingValue{Value: x.Value, Source: x.Source}
	}

	return &pb.SettingsEffective{
		TokenTtl: v(e.TokenTTL), RefreshLifetime: v(e.RefreshLifetime), DriftPolicy: v(e.DriftPolicy),
	}
}

func specFieldsPB(spec *ichorv1alpha1.IchorInstallationSpec) *pb.SettingsFields {
	out := &pb.SettingsFields{}

	if u := spec.Users; u != nil {
		if u.TokenTTL != nil {
			v := u.TokenTTL.Duration.String()
			out.TokenTtl = &v
		}

		if u.RefreshLifetime != nil {
			v := u.RefreshLifetime.Duration.String()
			out.RefreshLifetime = &v
		}
	}

	if d := spec.Defaults; d != nil && d.DriftPolicy != "" {
		out.DriftPolicy = &d.DriftPolicy
	}

	return out
}

func (s *Server) settingsResponse(ctx context.Context, inst *ichorv1alpha1.IchorInstallation) (*pb.GetSettingsResponse, error) {
	manager := ichorv1alpha1.IchorManagerInstaller
	if inst.HelmManaged() {
		manager = ichorv1alpha1.IchorManagerHelm
	}

	resp := &pb.GetSettingsResponse{
		Version: inst.Spec.Version, InstalledVersion: inst.Status.InstalledVersion, Manager: manager,
		ReadOnly: s.settingsReadOnly(), Spec: specFieldsPB(&inst.Spec),
		Effective: statusEffectivePB(inst.Status.Effective),
	}

	// ichor-ui's own view of the values it applies; the CLI's in-process server has none.
	if s.Settings != nil {
		e, err := s.Settings.Effective(ctx)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading the settings in force: %w", err))
		}

		resp.UiEffective = effectivePB(e)
	}

	if !resp.ReadOnly {
		var err error

		if resp.CanEditSettings, err = s.mayEditSetting(ctx, ichorv1alpha1.IchorSubresourceSettings); err != nil {
			return nil, err
		}
	}

	return resp, nil
}

func (s *Server) GetSettings(
	ctx context.Context, _ *connect.Request[pb.GetSettingsRequest],
) (*connect.Response[pb.GetSettingsResponse], error) {
	var inst ichorv1alpha1.IchorInstallation
	if err := s.Get(ctx, settingsKey(), &inst); err != nil {
		return nil, mapGetError("the IchorInstallation", err)
	}

	resp, err := s.settingsResponse(ctx, &inst)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func changesOf(f *pb.SettingsFields) (settings.Changes, error) {
	var c settings.Changes

	if f.ReclaimEnabled != nil {
		return c, errors.New("reclaim.enabled is removed: RBAC alone decides who may reset and add a machine (docs/adr/0107)")
	}

	c.DriftPolicy = f.DriftPolicy

	for _, d := range []struct {
		in  *string
		out **time.Duration
	}{{f.TokenTtl, &c.TokenTTL}, {f.RefreshLifetime, &c.RefreshLifetime}} {
		if d.in == nil {
			continue
		}

		v, err := time.ParseDuration(*d.in)
		if err != nil {
			return c, err
		}

		*d.out = &v
	}

	return c, nil
}

func (s *Server) UpdateSettings(
	ctx context.Context, req *connect.Request[pb.UpdateSettingsRequest],
) (*connect.Response[pb.UpdateSettingsResponse], error) {
	bad := func(err error) error { return connect.NewError(connect.CodeInvalidArgument, err) }

	if s.settingsReadOnly() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("the settings are managed by the deployment (--settings-read-only); change them there"))
	}

	changes, err := changesOf(req.Msg.GetChanges())
	if err != nil {
		return nil, bad(err)
	}

	if changes.Empty() {
		return nil, bad(errors.New("no setting to change"))
	}

	if err := changes.Validate(); err != nil {
		return nil, bad(err)
	}

	// RBAC is judged per group before the write, so the message names the verb; the webhook repeats the check.
	for _, g := range []struct {
		touched bool
		sub     string
	}{
		{changes.TokenTTL != nil || changes.RefreshLifetime != nil || changes.DriftPolicy != nil, ichorv1alpha1.IchorSubresourceSettings},
	} {
		if !g.touched {
			continue
		}

		ok, err := s.mayEditSetting(ctx, g.sub)
		if err != nil {
			return nil, err
		}

		if !ok {
			return nil, connect.NewError(connect.CodePermissionDenied,
				fmt.Errorf("RBAC does not grant you the \"update\" verb on ichorinstallations/%s", g.sub))
		}
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := s.Get(ctx, settingsKey(), &inst); err != nil {
		return nil, mapGetError("the IchorInstallation", err)
	}

	changes.Apply(&inst)

	if err := s.Update(ctx, &inst); err != nil {
		return nil, mapOperationWriteError("updating", settingsKey(), err)
	}

	resp, err := s.settingsResponse(ctx, &inst)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.UpdateSettingsResponse{Settings: resp}), nil
}
