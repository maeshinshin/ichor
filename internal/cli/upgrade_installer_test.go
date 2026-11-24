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
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// withInstallerFlags sets the installer image flags of `ichor upgrade ichor` for one test.
func withInstallerFlags(t *testing.T, digest string, skip bool) {
	t.Helper()

	oldDigest, oldSkip := upgradeInstallerDigest, upgradeSkipSignature
	upgradeInstallerDigest, upgradeSkipSignature = digest, skip

	t.Cleanup(func() { upgradeInstallerDigest, upgradeSkipSignature = oldDigest, oldSkip })
}

func TestInstallerOptionsOnlyForAnIchorUpgrade(t *testing.T) {
	withInstallerFlags(t, "sha256:"+strings.Repeat("a", 64), true)

	if o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_TALOS, ""); o != nil {
		t.Errorf("a Talos upgrade carries installer options: %v", o)
	}

	o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_ICHOR, "")
	if o.GetDigest() != "sha256:"+strings.Repeat("a", 64) || !o.GetSkipSignature() {
		t.Errorf("options = %v", o)
	}

	// With no flags an Ichor upgrade still sends the (empty) options, which clears an earlier pin or skip.
	withInstallerFlags(t, "", false)

	if o := installerOptionsFor(pb.OperationKind_OPERATION_KIND_ICHOR, ""); o == nil || o.GetDigest() != "" || o.GetSkipSignature() {
		t.Errorf("options without flags = %v", o)
	}
}

// --skip-signature-check is a bare boolean flag: no reason is asked for, and it takes no argument.
func TestSkipSignatureCheckIsABareFlag(t *testing.T) {
	f := upgradeIchorCmd.Flags().Lookup("skip-signature-check")
	if f == nil || f.Value.Type() != "bool" {
		t.Fatalf("--skip-signature-check = %+v, want a bool flag", f)
	}

	withInstallerFlags(t, "", true)

	if err := checkInstallerFlags(); err != nil {
		t.Errorf("a bare --skip-signature-check: err = %v", err)
	}
}

func TestInstallerDigestFlagMustBeASha256Digest(t *testing.T) {
	for _, bad := range []string{"latest", "sha256:abc", "sha256:" + strings.Repeat("A", 64), "sha512:" + strings.Repeat("a", 128)} {
		withInstallerFlags(t, bad, false)

		if err := checkInstallerFlags(); err == nil {
			t.Errorf("--installer-digest %q was accepted", bad)
		}
	}

	withInstallerFlags(t, "sha256:"+strings.Repeat("0", 64), false)

	if err := checkInstallerFlags(); err != nil {
		t.Errorf("a digest: err = %v", err)
	}
}

// The plan the server returns carries the warning for a skipped check; planText prints it before the
// confirmation (and with --yes), so the opt-out is shown, not silent.
func TestPlanTextShowsTheSkippedSignatureWarning(t *testing.T) {
	text := planText(pb.OperationKind_OPERATION_KIND_ICHOR, "c", &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_NONE, Current: testSettingsVersion, Target: noticeNewer,
		Warning: "The signature of the installer image is NOT checked, and the installer Job holds nearly cluster-admin rights.",
	})

	if !strings.Contains(text, "Warning: The signature of the installer image is NOT checked") {
		t.Fatalf("plan text = %q", text)
	}
}

// A same-version --skip-signature-check upgrade runs no installer Job, so the image is empty: the line
// must still read as a sentence, not start with "; ".
func TestVersionSkipWithoutAnInstallerImageHasNoStraySeparator(t *testing.T) {
	if got, want := installerImageText(&versionInstaller{SkipNext: true}), "the next upgrade will NOT check the signature"; got != want {
		t.Errorf("empty image with skip = %q, want %q", got, want)
	}
}

// With no installer Job run, the warning must not run on from the "installer image" label: the label
// shows a dash and the warning sits on a line of its own, aligned under the values.
func TestVersionSkipWithoutAnInstallerImageSitsOnItsOwnLine(t *testing.T) {
	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{
			Version:            noticeNewer,
			InstallerSignature: &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip},
		},
		Status: ichorv1alpha1.IchorInstallationStatus{InstalledVersion: noticeNewer},
	}

	info, _ := collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(), testCLIVersion)

	out := renderVersion(t, info, outputHuman)
	if !strings.Contains(out, "installer image -\n") || !strings.Contains(out, "\n"+strings.Repeat(" ", 16)+"the next upgrade will NOT check the signature\n") {
		t.Errorf("skip with no installer image:\n%s", out)
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
			Version:            noticeNewer,
			InstallerSignature: &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip},
		},
		Status: ichorv1alpha1.IchorInstallationStatus{
			InstalledVersion: noticeNewer,
			InstallerImage: &ichorv1alpha1.IchorInstallerImageStatus{
				Digest: "sha256:" + strings.Repeat("a", 64), Verification: ichorv1alpha1.InstallerImageSkipped,
			},
		},
	}

	info, _ := collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(), testCLIVersion)

	out := renderVersion(t, info, outputHuman)
	for _, want := range []string{"signature NOT checked", "sha256:" + strings.Repeat("a", 64), "the next upgrade will NOT check the signature"} {
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

// --skip-signature-check is not a one-shot bypass: it writes spec.installerSignature into the IchorInstallation.
// The help of every command that takes it has to say so, and how to undo it.
func TestSkipSignatureCheckHelpSaysTheChangePersists(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{"upgrade_ichor": upgradeIchorCmd, "init": initCmd} {
		t.Run(name, func(t *testing.T) {
			f := cmd.Flags().Lookup("skip-signature-check")
			if f == nil {
				t.Fatalf("ichor %s has no --skip-signature-check", name)
			}

			if !strings.Contains(f.Usage, "not one-shot") || !strings.Contains(f.Usage, "spec.installerSignature") {
				t.Errorf("the flag help does not say the skip is recorded: %q", f.Usage)
			}

			for _, want := range []string{"does not apply to one run only", "spec.installerSignature.mode: skip",
				"checks the signature again", "kubectl edit ichorinstallation ichor", "delete spec.installerSignature",
				"ichorinstallations/version", "Cilium Helm Job"} {
				if !strings.Contains(cmd.Long, want) {
					t.Errorf("the help does not contain %q", want)
				}
			}
		})
	}
}

// An Ichor upgrade rewrites the pin too; `ichor init` has no earlier pin to clear, so only the upgrade says so.
func TestUpgradeIchorHelpSaysTheFlagClearsThePin(t *testing.T) {
	if !strings.Contains(upgradeIchorCmd.Long, "clears it when the flag") {
		t.Errorf("the help of `ichor upgrade ichor` does not say the pin is rewritten: %q", upgradeIchorCmd.Long)
	}

	if strings.Contains(initCmd.Long, "clears it when the flag") {
		t.Error("the help of `ichor init` carries the upgrade's sentence about the pin")
	}
}

// Only Talos and Kubernetes have a recommended version; an Ichor upgrade has none and no --list.
func TestUpgradeIchorRequiresAVersion(t *testing.T) {
	if err := upgradeIchorCmd.Args(upgradeIchorCmd, nil); err == nil {
		t.Error("`ichor upgrade ichor` accepted no VERSION")
	}

	if strings.Contains(upgradeIchorCmd.Long, "Without VERSION") || strings.Contains(upgradeIchorCmd.Long, "--list") {
		t.Errorf("the help of `ichor upgrade ichor` describes a recommended version: %q", upgradeIchorCmd.Long)
	}

	if !strings.Contains(upgradeTalosCmd.Long, "Without VERSION it uses the recommended version") {
		t.Errorf("the help of `ichor upgrade talos` lost its recommended-version sentence: %q", upgradeTalosCmd.Long)
	}
}

// A verified upgrade after an unchecked one clears the skip and rewrites installerImage; the
// InstallerImageUnverified condition is what still says an unchecked image once ran, and
// `ichor version` shows it in every format.
func TestVersionSaysAnUncheckedImageOnceRanAfterAVerifiedUpgrade(t *testing.T) {
	scheme, err := cliScheme()
	if err != nil {
		t.Fatal(err)
	}

	first := metav1.NewTime(time.Date(2026, 11, 3, 4, 5, 6, 0, time.UTC))
	inst := &ichorv1alpha1.IchorInstallation{
		Name: ichorv1alpha1.IchorInstallationName,
		Spec: ichorv1alpha1.IchorInstallationSpec{Version: noticeNewer},
		Status: ichorv1alpha1.IchorInstallationStatus{
			InstalledVersion: noticeNewer,
			InstallerImage: &ichorv1alpha1.IchorInstallerImageStatus{
				Digest: "sha256:" + strings.Repeat("b", 64), Verification: ichorv1alpha1.InstallerImageVerified,
			},
			Conditions: []metav1.Condition{{
				Type: ichorv1alpha1.ConditionInstallerImageUnverified, Status: metav1.ConditionTrue, Reason: "SignatureSkipped",
				LastTransitionTime: first,
			}},
		},
	}

	info, _ := collectVersion(context.Background(), fake.NewClientBuilder().WithScheme(scheme).WithObjects(inst).Build(), testCLIVersion)

	out := renderVersion(t, info, outputHuman)
	for _, want := range []string{"signature verified", "an image whose signature was NOT checked has run here, first on 2026-11-03"} {
		if !strings.Contains(out, want) {
			t.Errorf("ichor version does not say %q:\n%s", want, out)
		}
	}

	if got := renderVersion(t, info, outputJSON); !strings.Contains(got, `"uncheckedImageRanSince": "2026-11-03T04:05:06Z"`) {
		t.Errorf("json output:\n%s", got)
	}
}
