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
	"slices"

	"connectrpc.com/connect"
	"golang.org/x/mod/semver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
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
	if kind != pb.OperationKind_OPERATION_KIND_TALOS && kind != pb.OperationKind_OPERATION_KIND_KUBERNETES &&
		kind != pb.OperationKind_OPERATION_KIND_CNI {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("ListUpgradeTargets lists the talos, kubernetes or cni kind, not %s", kind))
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

	lists, reason := upgradeLists(&inst, found)
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
func upgradeLists(inst *ichorv1alpha1.IchorInstallation, found bool) (*ichorv1alpha1.IchorUpgradeTargets, string) {
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

	if ut.Error != "" {
		return ut, fmt.Sprintf(reasonListFailedFm, ut.Error)
	}

	return ut, ""
}

// upgradeChoices groups the recorded releases of kind against the cluster (release.TalosChoices,
// release.KubernetesChoices and, for the cni kind, the Cilium patches of release.CiliumChoices). Without a
// list there is nothing to group.
func upgradeChoices(kind pb.OperationKind, cluster *ichorv1alpha1.TalosCluster, lists *ichorv1alpha1.IchorUpgradeTargets) release.Choices {
	if lists == nil {
		return release.Choices{}
	}

	switch kind {
	case pb.OperationKind_OPERATION_KIND_TALOS:
		return release.TalosChoices(cluster.Spec.TalosVersion, cluster.Spec.KubernetesVersion, release.Pinned.Talos, lists.Talos)
	case pb.OperationKind_OPERATION_KIND_CNI:
		installed, _ := ciliumVersion(cluster)
		if installed == "" {
			return release.Choices{}
		}

		return release.CiliumChoices("v"+installed, cilium.ChartVersion, cluster.Spec.KubernetesVersion, lists.Cilium)
	}

	installed, _ := ciliumVersion(cluster)

	// An observed non-Cilium CNI has no Cilium to match (ADR 0115, amendment 3); an unobserved one still
	// offers nothing until Cilium is seen.
	ciliumConstrains := cluster.Status.CNI == nil || cluster.Status.CNI.Detected == "" || cluster.Status.CNI.Detected == "cilium"

	return release.KubernetesChoices(cluster.Spec.KubernetesVersion, cluster.Spec.TalosVersion, installed, release.Pinned.Kubernetes,
		ciliumConstrains, lists.Kubernetes)
}

// ciliumPatchOffer says whether the cluster can take the Cilium patch now. When it is not offered only because the
// patch's Cilium minor is not tested with the cluster's Kubernetes, compat is that incompatibility: the one refusal the
// override verb lifts (ADR 0115, amendment). An unverified, not newer or unknown patch is never overridable. reason is
// why the list cannot be used ("" when it can).
func (s *Server) ciliumPatchOffer(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, version string,
) (offered bool, compat error, reason string, err error) {
	var inst ichorv1alpha1.IchorInstallation

	found := true

	switch err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); {
	case apierrors.IsNotFound(err):
		found = false
	case err != nil:
		return false, nil, "", mapGetError("IchorInstallation \""+ichorv1alpha1.IchorInstallationName+"\"", err)
	}

	lists, reason := upgradeLists(&inst, found)
	choices := upgradeChoices(pb.OperationKind_OPERATION_KIND_CNI, cluster, lists)

	if slices.Contains(append(slices.Clone(choices.Recommended), choices.Patch...), version) {
		return true, nil, reason, nil
	}

	installed, _ := ciliumVersion(cluster)
	if lists == nil || installed == "" {
		return false, nil, reason, nil
	}

	// The same verified, newer patches of the running minor, without the Kubernetes check.
	relaxed := release.Choose("v"+installed, "", lists.Cilium, func(to string) bool { return semver.Compare("v"+cilium.ChartVersion, to) < 0 })
	if !slices.Contains(append(slices.Clone(relaxed.Recommended), relaxed.Patch...), version) {
		return false, nil, reason, nil
	}

	if err := release.CiliumKubernetesError(version, cluster.Spec.KubernetesVersion); err != nil && !errors.Is(err, release.ErrUnknownVersion) {
		return false, err, reason, nil
	}

	return false, nil, reason, nil
}

