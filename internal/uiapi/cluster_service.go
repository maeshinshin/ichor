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
)

// ListClusters returns every TalosCluster the backend's own ServiceAccount
// can list (cluster.proto: "unset ... lists across all clusters the caller
// can see" -- MachineService's phrasing for the same thing; there is no
// per-caller RBAC yet, so "can see" is exactly "the backend's own RBAC
// permits," pending OIDC/impersonation (roadmap phase 9)).
func (s *Server) ListClusters(
	ctx context.Context, _ *connect.Request[pb.ListClustersRequest],
) (*connect.Response[pb.ListClustersResponse], error) {
	var list ichorv1alpha1.TalosClusterList
	if err := s.List(ctx, &list); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	clusters := make([]*pb.Cluster, len(list.Items))
	for i := range list.Items {
		clusters[i] = clusterToPB(&list.Items[i])
	}

	return connect.NewResponse(&pb.ListClustersResponse{Clusters: clusters}), nil
}

// GetCluster returns one TalosCluster. One Cluster message serves both the
// list and this call (cluster.proto's own doc comment), so there is
// nothing here beyond the lookup clusterToPB doesn't already do.
func (s *Server) GetCluster(
	ctx context.Context, req *connect.Request[pb.GetClusterRequest],
) (*connect.Response[pb.GetClusterResponse], error) {
	c, err := s.loadCluster(ctx, namespacedName(req.Msg.GetCluster()))
	if err != nil {
		return nil, err
	}

	out := clusterToPB(c)
	s.fillDiscoveryFindings(ctx, out, c)

	return connect.NewResponse(&pb.GetClusterResponse{Cluster: out}), nil
}
