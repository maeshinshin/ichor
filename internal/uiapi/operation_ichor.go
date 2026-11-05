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
	"regexp"
	"strconv"

	"connectrpc.com/connect"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/releasecheck"
)

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

	resp := &pb.GetIchorInstallationResponse{
		InstalledVersion: installed,
		TargetVersion:    inst.Spec.Version,
		Upgrading:        upgrading,
		// Helm moves a Helm-installed Ichor; spec.version only records the version at creation.
		UpdateAvailable: !upgrading && !inst.HelmManaged() && semver.IsValid(installed) && semver.IsValid(inst.Spec.Version) &&
			semver.Compare(inst.Spec.Version, installed) > 0,
	}

	if e := inst.Status.Effective; e != nil && e.AutoPatch != nil {
		resp.AutoPatchEnabled, _ = strconv.ParseBool(e.AutoPatch.Value)
	}

	if a := inst.Status.AutoPatch; a != nil {
		resp.AutoPatchHaltedOperation = a.HaltedOperation
	}

	// The installer image of the last upgrade and whether anything was checked.
	if img := inst.Status.InstallerImage; img != nil {
		resp.InstallerDigest, resp.InstallerVerification = img.Digest, img.Verification
	}

	resp.InstallerSignatureSkip = inst.Spec.InstallerSignature != nil && inst.Spec.InstallerSignature.Mode == ichorv1alpha1.InstallerSignatureSkip

	// What the controller's release check last found; the Upgrade flow does not use it.
	if rel := inst.Status.LatestRelease; rel != nil {
		resp.LatestVersion = rel.Version
		resp.ReleaseAvailable = releasecheck.Newer(installed, rel.Version)
		resp.ReleaseCheckError = rel.Error

		if rel.CheckedAt != nil {
			resp.ReleaseCheckedAt = timestamppb.New(rel.CheckedAt.Time)
		}
	}

	return connect.NewResponse(resp), nil
}

var installerDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// checkInstallerOptions refuses installer settings the CRD would refuse, before anything is written.
func checkInstallerOptions(o *pb.IchorInstallerOptions) error {
	if d := o.GetDigest(); d != "" && !installerDigestPattern.MatchString(d) {
		return invalid("the installer digest %q is not sha256: followed by 64 lowercase hex characters", d)
	}

	return nil
}

// installerOptionsOf is the installer settings the IchorInstallation spec holds, in the request's form.
func installerOptionsOf(spec *ichorv1alpha1.IchorInstallationSpec) (pin string, skip bool) {
	sig := spec.InstallerSignature

	return spec.InstallerDigest, sig != nil && sig.Mode == ichorv1alpha1.InstallerSignatureSkip
}

// skipsSignatureCheck is true when o asks to skip the signature check: skip_signature, or the deprecated
// skip_signature_reason an older client still sends, whose text is not read.
func skipsSignatureCheck(o *pb.IchorInstallerOptions) bool {
	return o.GetSkipSignature() || o.GetSkipSignatureReason() != "" //nolint:staticcheck // the deprecated field is still honoured
}

// installerOptionsChange is true when writing o would change the spec.
func installerOptionsChange(inst *ichorv1alpha1.IchorInstallation, o *pb.IchorInstallerOptions) bool {
	pin, skip := installerOptionsOf(&inst.Spec)

	return pin != o.GetDigest() || skip != skipsSignatureCheck(o)
}

// applyInstallerOptions writes o to the spec exactly: an empty field clears the setting, so a pin or a
// skip never carries over from an earlier upgrade.
func applyInstallerOptions(spec *ichorv1alpha1.IchorInstallationSpec, o *pb.IchorInstallerOptions) {
	spec.InstallerDigest = o.GetDigest()
	spec.InstallerSignature = nil

	if skipsSignatureCheck(o) {
		spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}
	}
}

// installerSkipWarning is what the plan says about a skipped signature check.
const installerSkipWarning = "The signature of the installer image is NOT checked, and the installer Job holds nearly cluster-admin rights."
