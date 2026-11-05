package controller

import (
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// testCNIImage is a Cilium Job image as the controller builds it: the installer repository by digest.
const testCNIImage = "ghcr.io/maeshinshin/ichor-installer@" + testInstallerDigest

// The Cilium Helm Job runs the installer image by digest only, and never one the controller has not checked
// (ADR 0122, amendment 2026-11-05): the digest the last Ichor upgrade recorded when it is for the installed
// version and repository, otherwise the tag resolved and verified here with the upgrade's own verifier.
func TestCNIJobImageIsADigestTheControllerChecked(t *testing.T) {
	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		other  = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
		mirror = "mirror.example/ichor-installer"
	)

	repo := cniTestInstallation().Spec.InstallerRepository

	record := func(version, repository, digest string) *ichorv1alpha1.IchorInstallation {
		inst := cniTestInstallation()
		inst.Status.InstallerImage = &ichorv1alpha1.IchorInstallerImageStatus{Version: version, Repository: repository, Digest: digest}

		return inst
	}

	pinned := func(inst *ichorv1alpha1.IchorInstallation, pin string) *ichorv1alpha1.IchorInstallation {
		inst.Spec.InstallerDigest = pin

		return inst
	}

	skipped := cniTestInstallation()
	skipped.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip, Reason: "air-gapped"}

	for name, tc := range map[string]struct {
		inst       *ichorv1alpha1.IchorInstallation
		images     *fakeInstallerImages
		want       string
		wantErr    string // the pause reason; empty for success
		wantVerify int
	}{
		"no record: the tag is resolved and verified": {
			inst: cniTestInstallation(), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record for this version is used as is": {
			inst: record("v0.9.0", repo, digest), images: &fakeInstallerImages{digest: other}, want: repo + "@" + digest,
		},
		"record of another version: resolved and verified": {
			inst: record("v0.8.0", repo, other), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record of another repository: resolved and verified": {
			inst: record("v0.9.0", mirror, other), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record without a digest: resolved and verified": {
			inst: record("v0.9.0", repo, ""), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record that is not the pin: the pin is verified": {
			inst: pinned(record("v0.9.0", repo, other), digest), images: &fakeInstallerImages{digest: other}, want: repo + "@" + digest, wantVerify: 1,
		},
		"skip mode runs the digest without a check": {
			inst: skipped, images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest,
		},
		"unresolvable tag": {
			inst: cniTestInstallation(), images: &fakeInstallerImages{resolveErr: errors.New("no route")}, wantErr: reasonInstallerImageUnresolved,
		},
		"unverified digest": {
			inst: cniTestInstallation(), images: &fakeInstallerImages{digest: digest, verifyErr: errors.New("no cosign signature")},
			wantErr: reasonInstallerImageUnverified, wantVerify: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := cniJobImage(t.Context(), tc.images, tc.inst)

			if tc.wantErr != "" {
				if got != "" || pauseReason(err) != tc.wantErr {
					t.Fatalf("image, err = %q, %v; want no image and the reason %s", got, err, tc.wantErr)
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("image, err = %q, %v; want %q", got, err, tc.want)
			}

			if len(tc.images.verified) != tc.wantVerify {
				t.Fatalf("verified %d images, want %d", len(tc.images.verified), tc.wantVerify)
			}
		})
	}

	t.Run("no verifier: refused, never an unchecked image", func(t *testing.T) {
		got, err := cniJobImage(t.Context(), nil, cniTestInstallation())
		if got != "" || pauseReason(err) != reasonInstallerImageUnverified {
			t.Fatalf("image, err = %q, %v; want no image and %s", got, err, reasonInstallerImageUnverified)
		}
	})
}

// An image that cannot be verified starts no Job at all.
func TestAdvanceCNIHelmJobStartsNoJobForAnUnverifiedImage(t *testing.T) {
	c := cniFakeClient(t)
	cluster := cniTestCluster("")
	images := &fakeInstallerImages{digest: testInstallerDigest, verifyErr: errors.New("no cosign signature")}

	hash, _ := CNIValuesHash(cluster, specMode(cluster), 0)
	name := cniHelmJobName(cluster, hash)

	done, err := AdvanceCNIHelmJob(t.Context(), c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), images,
		nil, "", time.Time{})
	if done || pauseReason(err) != reasonInstallerImageUnverified {
		t.Fatalf("= %t, %v; want not done and the reason %s", done, err, reasonInstallerImageUnverified)
	}

	var job batchv1.Job
	if err := c.Get(t.Context(), types.NamespacedName{Namespace: cniJobNamespace, Name: name}, &job); !apierrors.IsNotFound(err) {
		t.Fatalf("a Job exists for an unverified image (get: %v)", err)
	}

	// Verified: the Job runs the digest.
	images.verifyErr = nil

	if _, err := AdvanceCNIHelmJob(t.Context(), c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), images,
		nil, "", time.Time{}); err != nil {
		t.Fatal(err)
	}

	if err := c.Get(t.Context(), types.NamespacedName{Namespace: cniJobNamespace, Name: name}, &job); err != nil {
		t.Fatal(err)
	}

	want := cniTestInstallation().Spec.InstallerRepository + "@" + testInstallerDigest
	if got := job.Spec.Template.Spec.Containers[0].Image; got != want {
		t.Fatalf("Job image = %q, want %q", got, want)
	}
}
