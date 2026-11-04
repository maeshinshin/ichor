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

package cli

import (
	"context"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// withInstallerFlags sets the installer image flags of `ichor upgrade ichor` for one test.
func withInstallerFlags(t *testing.T, digest, skip string) {
	t.Helper()

	oldDigest, oldSkip := upgradeInstallerDigest, upgradeSkipSignature
	upgradeInstallerDigest, upgradeSkipSignature = digest, skip

	t.Cleanup(func() { upgradeInstallerDigest, upgradeSkipSignature = oldDigest, oldSkip })
}

func TestInstallerOptionsOnlyForAnIchorUpgrade(t *testing.T) {
	withInstallerFlags(t, "sha256:"+strings.Repeat("a", 64), "demo build")

	if o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_TALOS); o != nil {
		t.Errorf("a Talos upgrade carries installer options: %v", o)
	}

	o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_ICHOR)
	if o.GetDigest() != "sha256:"+strings.Repeat("a", 64) || o.GetSkipSignatureReason() != "demo build" {
		t.Errorf("options = %v", o)
	}

	// With no flags an Ichor upgrade still sends the (empty) options, which clears an earlier pin or skip.
	withInstallerFlags(t, "", "")

	if o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_ICHOR); o == nil || o.GetDigest() != "" || o.GetSkipSignatureReason() != "" {
		t.Errorf("options without flags = %v", o)
	}
}

func TestSkipSignatureCheckNeedsAReason(t *testing.T) {
	withInstallerFlags(t, "", "")

	err := checkInstallerFlags(true)
	if err == nil || !strings.Contains(err.Error(), "needs a reason") {
		t.Fatalf("a bare --skip-signature-check: err = %v", err)
	}

	withInstallerFlags(t, "", "   ")

	if err := checkInstallerFlags(true); err == nil {
		t.Error("a blank reason was accepted")
	}

	withInstallerFlags(t, "", "demo build")

	if err := checkInstallerFlags(true); err != nil {
		t.Errorf("a reason: err = %v", err)
	}

	withInstallerFlags(t, "", "")

	if err := checkInstallerFlags(false); err != nil {
		t.Errorf("no flag: err = %v", err)
	}
}

func TestInstallerDigestFlagMustBeASha256Digest(t *testing.T) {
	for _, bad := range []string{"v0.2.0", "sha256:abc", "sha256:" + strings.Repeat("A", 64), "sha512:" + strings.Repeat("a", 128)} {
		withInstallerFlags(t, bad, "")

		if err := checkInstallerFlags(false); err == nil {
			t.Errorf("--installer-digest %q was accepted", bad)
		}
	}

	withInstallerFlags(t, "sha256:"+strings.Repeat("0", 64), "")

	if err := checkInstallerFlags(false); err != nil {
		t.Errorf("a digest: err = %v", err)
	}
}

// The plan the server returns carries the warning for a skipped check; planText prints it before the
// confirmation (and with --yes), so the opt-out is shown, not silent.
func TestPlanTextShowsTheSkippedSignatureWarning(t *testing.T) {
	text := planText(pb.OperationKind_OPERATION_KIND_ICHOR, "c", &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_NONE, Current: "v0.1.0", Target: "v0.2.0",
		Warning: "The signature of the installer image is NOT checked, and the installer Job holds nearly cluster-admin rights: demo build.",
	})

	if !strings.Contains(text, "Warning: The signature of the installer image is NOT checked") {
		t.Fatalf("plan text = %q", text)
	}
}

func TestVersionShowsAnUncheckedInstallerImageLoudly(t *testing.T) {
	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:            "v0.2.0",
			InstallerSignature: &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip, Reason: "demo build"},
		},
		Status: ichorv1alpha1.IchorInstallationStatus{
			InstalledVersion: "v0.2.0",
			InstallerImage: &ichorv1alpha1.IchorInstallerImageStatus{
				Digest: "sha256:" + strings.Repeat("a", 64), Verification: ichorv1alpha1.InstallerImageSkipped, Reason: "demo build",
			},
		},
	}

	info, _ := collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(), testCLIVersion)

	out := renderVersion(t, info, outputHuman)
	for _, want := range []string{"signature NOT checked (demo build)", "sha256:" + strings.Repeat("a", 64), "the next upgrade will NOT check the signature"} {
		if !strings.Contains(out, want) {
			t.Errorf("ichor version does not say %q:\n%s", want, out)
		}
	}

	// A verified image says so, and a cluster with no upgrade yet shows no installer line at all.
	inst.Status.InstallerImage.Verification, inst.Spec.InstallerSignature = ichorv1alpha1.InstallerImageVerified, nil
	info, _ = collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst.DeepCopy()).Build(), testCLIVersion)

	if out := renderVersion(t, info, outputHuman); !strings.Contains(out, "signature verified") || strings.Contains(out, "NOT") {
		t.Errorf("a verified image:\n%s", out)
	}

	inst.Status.InstallerImage = nil
	info, _ = collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst.DeepCopy()).Build(), testCLIVersion)

	if out := renderVersion(t, info, outputHuman); strings.Contains(out, "installer image") {
		t.Errorf("no upgrade yet:\n%s", out)
	}
}
