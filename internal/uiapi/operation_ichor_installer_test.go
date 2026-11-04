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
	"strings"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// demoBuild is the reason the tests give for skipping the signature check.
const demoBuild = "demo build"

func getInstallation(t *testing.T, k8s client.Client) ichorv1alpha1.IchorInstallation {
	t.Helper()

	var inst ichorv1alpha1.IchorInstallation
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Fatal(err)
	}

	return inst
}

// ADR 0122: the installer settings are written exactly as the request gives them, even when the version
// is already the target (a retry with a skip after the controller refused the image), and an empty field
// clears them again so a skip never carries over to the next upgrade.
func TestIchorUpgradeWritesTheInstallerSettingsAndClearsThemNextTime(t *testing.T) {
	pin := "sha256:" + strings.Repeat("a", 64)
	srv, k8s := opServer(t, append(opObjects(), ichorInstallation(ichorOld, ichorOld))...)

	req := ichorCreate(ichorNew)
	req.Msg.IchorInstaller = &pb.IchorInstallerOptions{Digest: pin, SkipSignatureReason: demoBuild}

	if _, err := srv.CreateOperation(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	inst := getInstallation(t, k8s)
	if inst.Spec.InstallerDigest != pin || inst.Spec.InstallerSignature == nil ||
		inst.Spec.InstallerSignature.Mode != ichorv1alpha1.InstallerSignatureSkip || inst.Spec.InstallerSignature.Reason != demoBuild {
		t.Fatalf("spec = %+v", inst.Spec)
	}

	// The next upgrade, with the version already the target and no options, resets both.
	for _, op := range listOps(t, k8s) {
		if err := k8s.Delete(context.Background(), &op); err != nil {
			t.Fatal(err)
		}
	}

	res, err := srv.CreateOperation(context.Background(), ichorCreate(ichorNew))
	if err != nil {
		t.Fatal(err)
	}

	if !res.Msg.GetSpecChanged() {
		t.Error("clearing the installer settings is a spec change")
	}

	inst = getInstallation(t, k8s)
	if inst.Spec.Version != ichorNew || inst.Spec.InstallerDigest != "" || inst.Spec.InstallerSignature != nil {
		t.Fatalf("spec = %+v", inst.Spec)
	}
}

func TestIchorPlanWarnsWhenTheSignatureCheckIsSkipped(t *testing.T) {
	srv, _ := opServer(t, append(opObjects(), ichorInstallation(ichorOld, ichorOld))...)

	plan, err := srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: ichorNew,
		IchorInstaller: &pb.IchorInstallerOptions{SkipSignatureReason: demoBuild},
	}))
	if err != nil {
		t.Fatal(err)
	}

	if w := plan.Msg.GetWarning(); !strings.Contains(w, "NOT checked") || !strings.Contains(w, demoBuild) {
		t.Fatalf("warning = %q", w)
	}

	plan, err = srv.PlanOperation(context.Background(), connect.NewRequest(&pb.PlanOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Target: ichorNew,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if plan.Msg.GetWarning() != "" {
		t.Fatalf("a checked upgrade warns: %q", plan.Msg.GetWarning())
	}
}

func TestIchorUpgradeRefusesBadInstallerSettings(t *testing.T) {
	srv, k8s := opServer(t, append(opObjects(), ichorInstallation(ichorOld, ichorOld))...)

	for name, o := range map[string]*pb.IchorInstallerOptions{
		"a tag as the pin":   {Digest: "v0.2.0"},
		"an upper-case pin":  {Digest: "sha256:" + strings.Repeat("A", 64)},
		"a blank reason":     {SkipSignatureReason: "   "},
		"an overlong reason": {SkipSignatureReason: strings.Repeat("x", 513)},
	} {
		req := ichorCreate("v0.3.0")
		req.Msg.IchorInstaller = o

		if _, err := srv.CreateOperation(context.Background(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	if inst := getInstallation(t, k8s); inst.Spec.Version != ichorOld {
		t.Fatalf("a refused request wrote spec.version = %s", inst.Spec.Version)
	}

	// Installer settings belong to an Ichor upgrade only.
	req := connect.NewRequest(&pb.CreateOperationRequest{
		Cluster: objectRef(opNS, opCluster), Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: "v1.14.0",
		IchorInstaller: &pb.IchorInstallerOptions{SkipSignatureReason: "x"},
	})
	if _, err := srv.CreateOperation(context.Background(), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a Talos upgrade with installer settings: err = %v", err)
	}
}

func TestGetIchorInstallationShowsTheInstallerImage(t *testing.T) {
	inst := ichorInstallation(ichorNew, ichorOld)
	inst.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip, Reason: demoBuild}
	inst.Status.InstallerImage = &ichorv1alpha1.IchorInstallerImageStatus{
		Digest: "sha256:" + strings.Repeat("a", 64), Verification: ichorv1alpha1.InstallerImageSkipped, Reason: demoBuild,
	}

	srv, _ := opServer(t, inst)

	res, err := srv.GetIchorInstallation(context.Background(), connect.NewRequest(&pb.GetIchorInstallationRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	m := res.Msg
	if !m.GetInstallerSignatureSkip() || m.GetInstallerVerification() != "Skipped" || m.GetInstallerSkipReason() != demoBuild ||
		m.GetInstallerDigest() != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("got %v", m)
	}
}
