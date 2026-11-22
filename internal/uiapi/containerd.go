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

	"connectrpc.com/connect"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// SetContainerdDeviceOwnership sets spec.containerd.deviceOwnershipFromSecurityContext as the
// caller and touches nothing else (ADR 0172). The controller renders it into every node.
func (s *Server) SetContainerdDeviceOwnership(
	ctx context.Context, req *connect.Request[pb.SetContainerdDeviceOwnershipRequest],
) (*connect.Response[pb.SetContainerdDeviceOwnershipResponse], error) {
	key := namespacedName(req.Msg.GetCluster())

	c, err := s.loadCluster(ctx, key)
	if err != nil {
		return nil, err
	}

	if c.Spec.Containerd == nil {
		c.Spec.Containerd = &ichorv1alpha1.TalosClusterContainerd{}
	}

	c.Spec.Containerd.DeviceOwnershipFromSecurityContext = req.Msg.GetEnabled()

	if err := render.ValidateContainerd(c); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.Update(ctx, c, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("setting containerd device ownership on", key, err)
	}

	return connect.NewResponse(&pb.SetContainerdDeviceOwnershipResponse{
		Cluster: clusterToPB(c),
		Audit:   s.opAudit(ctx, "set-containerd-device-ownership", objectRef(c.Namespace, c.Name)),
	}), nil
}
