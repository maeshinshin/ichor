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

	"connectrpc.com/connect"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// clusterStep is one rollout step as the API reports it, and the operation kind it starts.
type clusterStep struct {
	step release.Step
	kind pb.OperationKind
	from string
	to   string
}

// planClusterUpgrade is the rollout for cluster: its steps in order (empty when it runs the release
// set) and what blocks starting one now (ADR 0113).
func (s *Server) planClusterUpgrade(ctx context.Context, ref *pb.ObjectRef) ([]clusterStep, string, error) {
	cluster, err := s.loadCluster(ctx, namespacedName(ref))
	if err != nil {
		return nil, "", err
	}

	configPending, err := s.anyConfigPending(ctx, cluster)
	if err != nil {
		return nil, "", err
	}

	blocked, err := s.unfinishedOperation(ctx, cluster)
	if err != nil {
		return nil, "", err
	}

	var steps []clusterStep

	for _, st := range release.Steps(runningOf(cluster, configPending)) {
		steps = append(steps, clusterStepOf(cluster, st))
	}

	return steps, blocked, nil
}

// clusterStepOf describes one step of cluster's rollout.
func clusterStepOf(cluster *ichorv1alpha1.TalosCluster, st release.Step) clusterStep {
	cur, _ := ciliumVersion(cluster)

	switch st {
	case release.StepTalos:
		return clusterStep{step: st, kind: pb.OperationKind_OPERATION_KIND_TALOS, from: cluster.Spec.TalosVersion, to: release.Pinned.Talos}
	case release.StepConfig:
		return clusterStep{step: st, kind: pb.OperationKind_OPERATION_KIND_CONFIG}
	case release.StepKubernetes:
		return clusterStep{step: st, kind: pb.OperationKind_OPERATION_KIND_KUBERNETES, from: cluster.Spec.KubernetesVersion, to: release.Pinned.Kubernetes}
	default:
		return clusterStep{step: release.StepCilium, kind: pb.OperationKind_OPERATION_KIND_CNI, from: cur, to: release.Pinned.Cilium}
	}
}

// anyConfigPending reports whether an approved machine of cluster has a configuration change that
// needs a reboot or an API server restart (upgrade.ConfigPending).
func (s *Server) anyConfigPending(ctx context.Context, cluster *ichorv1alpha1.TalosCluster) (bool, error) {
	var list ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &list, client.InNamespace(cluster.Namespace)); err != nil {
		return false, mapListError("machines", err)
	}

	for i := range list.Items {
		m := &list.Items[i]
		if m.Spec.ClusterRef.Name == cluster.Name && m.Spec.IsApproved() && upgrade.ConfigPending(m) {
			return true, nil
		}
	}

	return false, nil
}

// PlanClusterUpgrade reports the steps that bring a cluster to the release set, without writing.
func (s *Server) PlanClusterUpgrade(
	ctx context.Context, req *connect.Request[pb.PlanClusterUpgradeRequest],
) (*connect.Response[pb.PlanClusterUpgradeResponse], error) {
	steps, blocked, err := s.planClusterUpgrade(ctx, req.Msg.GetCluster())
	if err != nil {
		return nil, err
	}

	out := &pb.PlanClusterUpgradeResponse{Blocked: blocked, ReleaseSet: releaseSetToPB()}
	for _, st := range steps {
		out.Steps = append(out.Steps, &pb.ClusterUpgradeStep{Step: string(st.step), From: st.from, To: st.to, Kind: st.kind})
	}

	return connect.NewResponse(out), nil
}

// StartClusterUpgrade creates the operation of the next step of the rollout, and only that one: the
// next step needs the previous one finished (ADR 0113). Each step is started as its own operation
// would be, so the same checks and the same audit apply.
func (s *Server) StartClusterUpgrade(
	ctx context.Context, req *connect.Request[pb.StartClusterUpgradeRequest],
) (*connect.Response[pb.StartClusterUpgradeResponse], error) {
	ref := req.Msg.GetCluster()

	steps, blocked, err := s.planClusterUpgrade(ctx, ref)
	if err != nil {
		return nil, err
	}

	if blocked != "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(blocked))
	}

	if len(steps) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("the cluster already runs the release set of this Ichor: nothing to upgrade"))
	}

	first := steps[0]

	var opRef *pb.ObjectRef

	var audit *pb.AuditRecord

	switch first.step {
	case release.StepCilium:
		resp, err := s.StartCniUpgrade(ctx, connect.NewRequest(&pb.StartCniUpgradeRequest{Cluster: ref}))
		if err != nil {
			return nil, err
		}

		opRef, audit = resp.Msg.GetOperation(), resp.Msg.GetAudit()
	default:
		resp, err := s.CreateOperation(ctx, connect.NewRequest(&pb.CreateOperationRequest{
			Cluster: ref, Kind: first.kind, Target: first.to,
		}))
		if err != nil {
			return nil, err
		}

		opRef, audit = resp.Msg.GetOperation().GetRef(), resp.Msg.GetAudit()
	}

	return connect.NewResponse(&pb.StartClusterUpgradeResponse{
		Step: string(first.step), Operation: opRef, Audit: audit,
	}), nil
}
