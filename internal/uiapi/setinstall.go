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

	"connectrpc.com/connect"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
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

	if !m.Spec.IsApproved() {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("node %q is not approved; its install disk is chosen when it is approved", namespacedName(ref)))
	}

	if m.Status.AppliedConfigHash != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("node %q already has a configuration applied; an install disk only applies before the first install", namespacedName(ref)))
	}

	install, err := installFromPB(req.Msg.GetInstall())
	if err == nil && install == nil {
		err = errors.New("install is required")
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	install, err = s.withInstallerImage(ctx, m, install)
	if err != nil {
		return nil, err
	}

	m.Spec.Install = install

	if err := s.Update(ctx, m, strictFieldValidation); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("setting install of node %q: %w", namespacedName(ref), err))
	}

	audit := s.opAudit(ctx, "set-install", objectRef(m.Namespace, m.Name))

	return connect.NewResponse(&pb.SetMachineInstallResponse{Machine: machineToPB(m), Audit: audit}), nil
}
