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

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

// The reasons a list cannot be given (ADR 0115). They are shown as they are.
const (
	reasonCheckOff     = "The release check is off: turn on the update check in Settings to list versions."
	reasonNoListYet    = "No release list yet: the first check has not run."
	reasonListFailedFm = "The last check could not list the releases: %s"
)

// ListUpgradeTargets lists the versions a cluster's Talos or Kubernetes can move to, from the lists the
// controller's release check recorded (ADR 0115), filtered against what the cluster runs now. It only
// reads. A list that cannot be given says why, and the UI still offers a typed version.
func (s *Server) ListUpgradeTargets(
	ctx context.Context, req *connect.Request[pb.ListUpgradeTargetsRequest],
) (*connect.Response[pb.ListUpgradeTargetsResponse], error) {
	kind := req.Msg.GetKind()
	if kind != pb.OperationKind_OPERATION_KIND_TALOS && kind != pb.OperationKind_OPERATION_KIND_KUBERNETES {
		return nil, invalid("ListUpgradeTargets lists the talos or kubernetes kind, not %s", kind)
	}

	cluster, err := s.loadCluster(ctx, namespacedName(req.Msg.GetCluster()))
	if err != nil {
		return nil, err
	}

	var inst ichorv1alpha1.IchorInstallation

	found := true

	switch err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); {
	case apierrors.IsNotFound(err):
		found = false
	case err != nil:
		return nil, mapGetError("IchorInstallation \""+ichorv1alpha1.IchorInstallationName+"\"", err)
	}

	lists, reason := upgradeLists(&inst, found, kind)
	choices := upgradeChoices(kind, cluster, lists)

	return connect.NewResponse(&pb.ListUpgradeTargetsResponse{
		Recommended:       choices.Recommended,
		Patch:             choices.Patch,
		NextMinor:         choices.NextMinor,
		Default:           choices.Default(),
		Available:         reason == "",
		UnavailableReason: reason,
	}), nil
}

// upgradeLists is the recorded list the server filters, and why it cannot be used ("" when it can).
// A list with an error is still returned: its last good value is what the controller kept, and the
// reason says it may be stale.
func upgradeLists(inst *ichorv1alpha1.IchorInstallation, found bool, kind pb.OperationKind) (*ichorv1alpha1.IchorUpgradeTargets, string) {
	if !found {
		return nil, reasonNoListYet
	}

	if e := inst.Status.Effective; e != nil && e.UpdateCheck != nil && e.UpdateCheck.Value == "false" {
		return nil, reasonCheckOff
	}

	ut := inst.Status.UpgradeTargets
	if ut == nil {
		return nil, reasonNoListYet
	}

	if e := listError(ut, kind); e != "" {
		return ut, fmt.Sprintf(reasonListFailedFm, e)
	}

	return ut, ""
}

// listError is why the list of kind could not be listed, "" when it could. Each list has its own error, so a
// failing repository does not make the others unavailable (ADR 0115, amendment 2026-11-04). A status written
// by an earlier controller has only the joined Error; it counts against every list.
func listError(ut *ichorv1alpha1.IchorUpgradeTargets, kind pb.OperationKind) string {
	if ut.TalosError == "" && ut.KubernetesError == "" {
		return ut.Error
	}

	if kind == pb.OperationKind_OPERATION_KIND_TALOS {
		return ut.TalosError
	}

	return ut.KubernetesError
}

// upgradeChoices groups the recorded releases of kind against the cluster (release.TalosChoices,
// release.KubernetesChoices). Without a list there is nothing to group.
func upgradeChoices(kind pb.OperationKind, cluster *ichorv1alpha1.TalosCluster, lists *ichorv1alpha1.IchorUpgradeTargets) release.Choices {
	if lists == nil {
		return release.Choices{}
	}

	switch kind {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return release.TalosChoices(cluster.Spec.TalosVersion, cluster.Spec.KubernetesVersion, release.Pinned.Talos, lists.Talos)
	}

	installed, _ := ciliumVersion(cluster)

	// An observed non-Cilium CNI has no Cilium to match (ADR 0115, amendment 3); an unobserved one still
	// offers nothing until Cilium is seen.
	ciliumConstrains := cluster.Status.CNI == nil || cluster.Status.CNI.Detected == "" || cluster.Status.CNI.Detected == "cilium"

	return release.KubernetesChoices(cluster.Spec.KubernetesVersion, cluster.Spec.TalosVersion, installed, release.Pinned.Kubernetes,
		ciliumConstrains, lists.Kubernetes)
}
