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

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
)

// PlanClusterUpgrade is deprecated and unused: the cluster-wide rollout is removed, and a
// cluster moves per component with ListUpgradeTargets and the per-component operations. The proto
// keeps the RPC for the wire format, and the generated Unimplemented handler answers it.
func (s *Server) PlanClusterUpgrade( //nolint:staticcheck // deprecated RPC
	ctx context.Context, req *connect.Request[pb.PlanClusterUpgradeRequest], //nolint:staticcheck // deprecated
) (*connect.Response[pb.PlanClusterUpgradeResponse], error) { //nolint:staticcheck // deprecated
	return ichorv1alpha1connect.UnimplementedOperationServiceHandler{}.PlanClusterUpgrade(ctx, req)
}

// StartClusterUpgrade is deprecated and unused; see PlanClusterUpgrade.
func (s *Server) StartClusterUpgrade( //nolint:staticcheck // deprecated RPC
	ctx context.Context, req *connect.Request[pb.StartClusterUpgradeRequest], //nolint:staticcheck // deprecated
) (*connect.Response[pb.StartClusterUpgradeResponse], error) { //nolint:staticcheck // deprecated
	return ichorv1alpha1connect.UnimplementedOperationServiceHandler{}.StartClusterUpgrade(ctx, req)
}
