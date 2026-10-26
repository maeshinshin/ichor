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
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

// SetMachineInstall writes only spec.install of an approved machine that has not converged yet: how a machine
// approved without an install disk (Ready reason InstallDiskUnknown)
// gets one. Once the machine has an applied configuration an install target no longer applies
// (Talos is already installed), so that is refused rather than silently ignored.
func (s *Server) SetMachineInstall(
	ctx context.Context, req *connect.Request[pb.SetMachineInstallRequest],
) (*connect.Response[pb.SetMachineInstallResponse], error) {
	ref := req.Msg.GetMachine()

	m, err := s.getMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	plan, err := s.planWrite(ctx, m, m.Spec.ClusterRef.Name, false, req.Msg.GetBreakGlass())
	if err != nil {
		return nil, err
	}

	if !m.Spec.IsApproved() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q is not approved; its install disk is chosen when it is approved", namespacedName(ref)))
	}

	if m.Status.AppliedConfigHash != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q already has a configuration applied; an install disk only applies before the first install", namespacedName(ref)))
	}

	install, err := installFromPB(req.Msg.GetInstall())
	if err == nil && install == nil {
		err = errors.New("install is required")
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	now := s.now()
	stored := m.DeepCopy()
	m.Spec.Install = install

	if plan != nil && plan.bg == nil {
		return s.setInstallByPR(ctx, plan, m, stored, now, req.Msg.GetReason())
	}

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("setting install of machine %q: %w", namespacedName(ref), err))
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "set-install",
		Target: objectRef(m.Namespace, m.Name), Reason: req.Msg.GetReason(),
	}

	url, notice := s.afterBreakGlass(ctx, plan, m, audit, func() (string, error) {
		r, err := s.setInstallByPR(ctx, plan, m, stored, now, req.Msg.GetReason())
		if err != nil {
			return "", err
		}

		return r.Msg.GetPullRequestUrl(), nil
	})

	return connect.NewResponse(&pb.SetMachineInstallResponse{Machine: machineToPB(m), Audit: audit, PullRequestUrl: url, Notice: notice}), nil
}

// setInstallByPR is SetMachineInstall's pull request branch: it edits spec.install only.
func (s *Server) setInstallByPR(
	ctx context.Context, plan *prPlan, m, stored *ichorv1alpha1.TalosMachine, now time.Time, reason string,
) (*connect.Response[pb.SetMachineInstallResponse], error) {
	install, err := toGeneric(m.Spec.Install)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	url, err := s.openPR(ctx, plan, prChange{
		verb: "set-install", name: m.Name, target: machineTarget(m),
		updates: []prmode.FieldUpdate{{Path: []string{specKey, "install"}, Value: install}},
		title:   "Set install disk of node " + m.Name,
		impact: fmt.Sprintf("- Affected nodes: 1 (%s)\n- Sets where the node installs Talos; the controller applies it once synced.\n- Reason: %s",
			m.Name, orNone(reason)),
	})
	if err != nil {
		return nil, err
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(ctx), At: timestamppb.New(now), Action: "set-install-pr", Target: objectRef(m.Namespace, m.Name), Reason: reason,
	}

	return connect.NewResponse(&pb.SetMachineInstallResponse{Machine: machineToPB(stored), Audit: audit, PullRequestUrl: url}), nil
}
