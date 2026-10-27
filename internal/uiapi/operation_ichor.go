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
	"golang.org/x/mod/semver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/prmode"
)

const kindIchorInstallation = "IchorInstallation"

// upgradeIchorByPR is CreateOperation for an Ichor upgrade whose IchorInstallation Argo CD
// tracks. The file that holds the IchorInstallation in Git gets the new
// spec.version, and that is all: no operation is created, because the cluster does not move
// until the pull request is merged. Once it is merged and synced, an operation without a target
// runs the installer Job (mode ichor); in mode external Argo CD applies Ichor's own manifests,
// which the pull request does not touch (see the description it carries).
func (s *Server) upgradeIchorByPR(ctx context.Context, p *opPlan) (*connect.Response[pb.CreateOperationResponse], error) {
	current, target := p.install.Spec.Version, p.resp.GetTarget()

	next := "Merge it, wait for Argo CD to sync, then start the upgrade again without a target: the installer Job runs the new version."
	if p.install.Spec.ManagementMode == ichorv1alpha1.IchorManagementModeExternal {
		next = "Ichor is in external mode, so no installer Job runs: this pull request only records the target. " +
			"Bump the chart or manifests that Argo CD applies for Ichor to " + target + " as well."
	}

	url, err := s.openPR(ctx, p.pr, prChange{
		verb: "upgrade-ichor", name: target,
		target: prmode.Target{APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: kindIchorInstallation, Name: p.install.Name},
		updates: []prmode.FieldUpdate{{Path: []string{specKey, "version"}, Value: target}},
		title:   "Upgrade Ichor to " + target,
		impact:  fmt.Sprintf("- Changes IchorInstallation spec.version from %s to %s.\n- %s", current, target, next),
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CreateOperationResponse{
		PullRequestUrl: url,
		Audit:          s.opAudit(ctx, "upgrade-ichor-pr", objectRef("", p.install.Name)),
	}), nil
}

// GetIchorInstallation reports the installed version and whether a newer target is known. A
// cluster with no IchorInstallation (it was not created by `ichor init`) answers with empty
// fields, not an error: the UI shows no version label rather than a failure.
func (s *Server) GetIchorInstallation(
	ctx context.Context, _ *connect.Request[pb.GetIchorInstallationRequest],
) (*connect.Response[pb.GetIchorInstallationResponse], error) {
	var inst ichorv1alpha1.IchorInstallation

	switch err := s.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); {
	case apierrors.IsNotFound(err):
		return connect.NewResponse(&pb.GetIchorInstallationResponse{}), nil
	case err != nil:
		return nil, mapGetError("IchorInstallation \""+ichorv1alpha1.IchorInstallationName+"\"", err)
	}

	var ops ichorv1alpha1.TalosOperationList
	if err := s.List(ctx, &ops); err != nil {
		return nil, mapListError("operations", err)
	}

	upgrading := false

	for i := range ops.Items {
		o := &ops.Items[i]
		if o.Spec.Kind == ichorv1alpha1.TalosOperationKindIchor && o.Status.Phase != ichorv1alpha1.TalosOperationPhaseCompleted {
			upgrading = true
		}
	}

	installed := inst.Status.InstalledVersion

	return connect.NewResponse(&pb.GetIchorInstallationResponse{
		InstalledVersion: installed,
		TargetVersion:    inst.Spec.Version,
		ManagementMode:   inst.Spec.ManagementMode,
		Upgrading:        upgrading,
		UpdateAvailable: !upgrading && semver.IsValid(installed) && semver.IsValid(inst.Spec.Version) &&
			semver.Compare(inst.Spec.Version, installed) > 0,
	}), nil
}
