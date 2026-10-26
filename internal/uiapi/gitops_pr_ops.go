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
	"fmt"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"google.golang.org/protobuf/types/known/timestamppb"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

// approveByPR is ApproveMachine's pull request branch (docs/adr/0056): m is
// the approved machine as it would be written, stored the machine as it is
// now. Nothing is written to the cluster; the machine stays pending until
// the pull request merges and Argo CD syncs it.
func (s *Server) approveByPR(
	ctx context.Context, plan *prPlan, m, stored *ichorv1alpha1.TalosMachine, now time.Time, reason string,
) (*connect.Response[pb.ApproveMachineResponse], error) {
	change := prChange{
		verb: "approve", name: m.Name, target: machineTarget(m),
		title: fmt.Sprintf("Approve node %s as %s", m.Name, m.Spec.Role),
		impact: fmt.Sprintf("- Affected nodes: 1 (%s, endpoint %s, role %s)\n"+
			"- Approving lets the controller apply machine configuration to this node once the change is synced; "+
			"the node stays pending until then.\n- Reason: %s", m.Name, m.Spec.Endpoint, m.Spec.Role, orNone(reason)),
	}

	if plan.add {
		manifest, err := machineManifest(m)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("rendering machine %q: %w", m.Name, err))
		}

		change.newFile = &prmode.NewFile{Path: path.Join(plan.repo.MachinesPath, m.Name+".yaml"), Content: manifest}
	} else {
		updates, err := machineSpecUpdates(m)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		change.updates = updates
	}

	url, err := s.openPR(ctx, plan, change)
	if err != nil {
		return nil, err
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(), At: timestamppb.New(now), Action: "approve-pr", Target: objectRef(m.Namespace, m.Name), Reason: reason,
	}

	return connect.NewResponse(&pb.ApproveMachineResponse{Machine: machineToPB(stored), Audit: audit, PullRequestUrl: url}), nil
}

// rejectByPR is RejectMachine's pull request branch, for a machine Argo CD
// tracks.
func (s *Server) rejectByPR(
	ctx context.Context, plan *prPlan, m, _ *ichorv1alpha1.TalosMachine, now time.Time, reason string,
) (*connect.Response[pb.RejectMachineResponse], error) {
	updates, err := machineSpecUpdates(m)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	url, err := s.openPR(ctx, plan, prChange{
		verb: "reject", name: m.Name, target: machineTarget(m), updates: updates,
		title: "Reject node " + m.Name,
		impact: fmt.Sprintf("- Affected nodes: 1 (%s)\n- Marks the machine rejected; no configuration is applied to it.\n- Reason: %s",
			m.Name, orNone(reason)),
	})
	if err != nil {
		return nil, err
	}

	audit := &pb.AuditRecord{
		Actor: s.actorName(), At: timestamppb.New(now), Action: "reject-pr", Target: objectRef(m.Namespace, m.Name), Reason: reason,
	}

	return connect.NewResponse(&pb.RejectMachineResponse{Audit: audit, PullRequestUrl: url}), nil
}

// updatePatchByPR is UpdatePatch's pull request branch. A patch update is a
// full replace of spec (patch.proto), so the PR replaces the file's spec
// node as a whole; comments inside it are not kept (docs/adr/0056).
func (s *Server) updatePatchByPR(
	ctx context.Context, plan *prPlan, cluster *ichorv1alpha1.TalosCluster, p, stored *ichorv1alpha1.TalosConfigPatch,
) (*connect.Response[pb.UpdatePatchResponse], error) {
	spec, err := toGeneric(p.Spec)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	impact, err := patchImpactText(cluster, p)
	if err != nil {
		return nil, err
	}

	url, err := s.openPR(ctx, plan, prChange{
		verb: "update", name: p.Name, target: patchTarget(p),
		updates: []prmode.FieldUpdate{{Path: []string{specKey}, Value: spec}},
		title:   "Update patch " + p.Name, impact: impact,
	})
	if err != nil {
		return nil, err
	}

	out, err := patchToPB(stored)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	audit := &pb.AuditRecord{Actor: s.actorName(), At: timestamppb.New(s.now()), Action: "update-pr", Target: objectRef(p.Namespace, p.Name)}

	return connect.NewResponse(&pb.UpdatePatchResponse{Patch: out, Audit: audit, PullRequestUrl: url}), nil
}

// patchImpactText is PreviewImpact's answer for p, as the PR description
// carries it ("affected nodes, reboot required or not"). cluster may be nil
// (not found): the layer and target are still said, the field impact is
// skipped.
func patchImpactText(cluster *ichorv1alpha1.TalosCluster, p *ichorv1alpha1.TalosConfigPatch) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "- Layer: %s", p.Spec.Layer)

	if t := p.Spec.Target; t != nil {
		switch {
		case t.Role != "":
			fmt.Fprintf(&b, ", role %s", t.Role)
		case t.MachineRef != nil:
			fmt.Fprintf(&b, ", machine %s", t.MachineRef.Name)
		}
	}

	b.WriteString("\n")

	if cluster == nil {
		return b.String(), nil
	}

	pinned, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}

	paths, err := patchPaths(p.Spec.Documents)
	if err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}

	impact := fieldmeta.Assess(paths, pinned)
	fmt.Fprintf(&b, "- Reboot required: %t\n", impact.RebootRequired)

	if len(impact.InstallTimeOnly) > 0 {
		fmt.Fprintf(&b, "- Install-time only (not applied to a running node): %s\n", strings.Join(impact.InstallTimeOnly, ", "))
	}

	if len(impact.Unsupported) > 0 {
		fmt.Fprintf(&b, "- Unsupported fields: %s\n", strings.Join(impact.Unsupported, ", "))
	}

	return b.String(), nil
}

func orNone(s string) string {
	if s == "" {
		return "(none given)"
	}

	return s
}
