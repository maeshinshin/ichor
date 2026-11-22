package controller

import (
	"errors"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// testCNIImage is a Cilium Job image as the controller builds it: the installer repository by digest.
const testCNIImage = "ghcr.io/maeshinshin/ichor-installer@" + testInstallerDigest

// The Cilium Helm Job runs the installer image by digest only, and never one the controller has not checked: the
// digest the last Ichor upgrade recorded when it is for the installed
// version and repository, otherwise the tag resolved and verified here with the upgrade's own verifier.
func TestCNIJobImageIsADigestTheControllerChecked(t *testing.T) {
	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		other  = "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
		mirror = "mirror.example/ichor-installer"
	)

	repo := cniTestInstallation().Spec.InstallerRepository

	recorded := func(version, repository, digest string) *ichorv1alpha1.IchorInstallation {
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

	// What `ichor init --installer-digest D --skip-signature-check` writes: a fresh install with no status
	// record, on a cluster that cannot reach the registry.
	initPinned := cniTestInstallation()
	initPinned.Spec.InstallerDigest = digest
	initPinned.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}

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
			inst: recorded("v0.9.0", repo, digest), images: &fakeInstallerImages{digest: other}, want: repo + "@" + digest,
		},
		"record of another version: resolved and verified": {
			inst: recorded("v0.8.0", repo, other), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record of another repository: resolved and verified": {
			inst: recorded("v0.9.0", mirror, other), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record without a digest: resolved and verified": {
			inst: recorded("v0.9.0", repo, ""), images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest, wantVerify: 1,
		},
		"record that is not the pin: the pin is verified": {
			inst: pinned(recorded("v0.9.0", repo, other), digest), images: &fakeInstallerImages{digest: other}, want: repo + "@" + digest, wantVerify: 1,
		},
		"skip mode runs the digest without a check": {
			inst: skipped, images: &fakeInstallerImages{digest: digest}, want: repo + "@" + digest,
		},
		"pinned by init with skip: no record and no registry needed": {
			inst: initPinned, images: &fakeInstallerImages{resolveErr: errors.New("no route")}, want: repo + "@" + digest,
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
			got, err := cniJobImage(t.Context(), cniFakeClient(t, tc.inst.DeepCopy()), tc.images, tc.inst, nil)

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
		got, err := cniJobImage(t.Context(), cniFakeClient(t), nil, cniTestInstallation(), nil)
		if got != "" || pauseReason(err) != reasonInstallerImageUnverified {
			t.Fatalf("image, err = %q, %v; want no image and %s", got, err, reasonInstallerImageUnverified)
		}
	})
}

// A Cilium Job that runs under skip leaves what an upgrade's skip leaves: the digest and Skipped in
// IchorInstallation status, and a Warning Event. A checked image leaves no Skipped record.
func TestCNIJobUnderSkipLeavesATrace(t *testing.T) {
	skipped := cniTestInstallation()
	skipped.Spec.InstallerSignature = &ichorv1alpha1.IchorInstallerSignature{Mode: ichorv1alpha1.InstallerSignatureSkip}

	read := func(c client.Client) *ichorv1alpha1.IchorInstallerImageStatus {
		t.Helper()

		var got ichorv1alpha1.IchorInstallation
		if err := c.Get(t.Context(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
			t.Fatal(err)
		}

		return got.Status.InstallerImage
	}

	t.Run("skip", func(t *testing.T) {
		c := cniFakeClient(t, skipped.DeepCopy())
		rec := record.NewFakeRecorder(5)

		inst := skipped.DeepCopy()
		if _, err := cniJobImage(t.Context(), c, &fakeInstallerImages{digest: testInstallerDigest}, inst, rec); err != nil {
			t.Fatal(err)
		}

		got := read(c)
		if got == nil || got.Verification != ichorv1alpha1.InstallerImageSkipped || got.Digest != testInstallerDigest ||
			got.Version != skipped.Spec.Version || got.Repository != skipped.Spec.InstallerRepository {
			t.Fatalf("status.installerImage = %+v; want Skipped for %s", got, testInstallerDigest)
		}

		select {
		case ev := <-rec.Events:
			for _, want := range []string{"Warning " + reasonInstallerSignatureSkipped, "NOT checked", testInstallerDigest} {
				if !strings.Contains(ev, want) {
					t.Fatalf("event %q lacks %q", ev, want)
				}
			}
		default:
			t.Fatal("no Warning Event")
		}
	})

	t.Run("checked", func(t *testing.T) {
		c := cniFakeClient(t, cniTestInstallation())
		rec := record.NewFakeRecorder(5)

		if _, err := cniJobImage(t.Context(), c, &fakeInstallerImages{digest: testInstallerDigest}, cniTestInstallation(), rec); err != nil {
			t.Fatal(err)
		}

		if got := read(c); got != nil || len(rec.Events) != 0 {
			t.Fatalf("a checked image left status %+v and %d events", got, len(rec.Events))
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

	done, err := AdvanceCNIHelmJob(t.Context(), c, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), images, nil,
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

	if _, err := AdvanceCNIHelmJob(t.Context(), c, c, "ichor-system", cluster, specMode(cluster), 0, cniTestInstallation(), images, nil,
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

// An image that ran unchecked leaves InstallerImageUnverified on the IchorInstallation, and a
// verified run afterwards, which rewrites status.installerImage, does not take it off.
func TestUnverifiedInstallerImageConditionIsSticky(t *testing.T) {
	inst := cniTestInstallation()
	c := cniFakeClient(t, inst.DeepCopy())

	read := func() *metav1.Condition {
		t.Helper()

		var got ichorv1alpha1.IchorInstallation
		if err := c.Get(t.Context(), types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &got); err != nil {
			t.Fatal(err)
		}

		return apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionInstallerImageUnverified)
	}

	record := func(verification string) {
		t.Helper()

		img := pinnedInstallerImage{Digest: testInstallerDigest, Verification: verification}
		if err := recordInstallerImage(t.Context(), c, inst.DeepCopy(), img, nil); err != nil {
			t.Fatal(err)
		}
	}

	record(ichorv1alpha1.InstallerImageVerified)

	if got := read(); got != nil {
		t.Fatalf("a verified run set %+v", got)
	}

	record(ichorv1alpha1.InstallerImageSkipped)

	got := read()
	if got == nil || got.Status != metav1.ConditionTrue || !strings.Contains(got.Message, testInstallerDigest) {
		t.Fatalf("after a skipped run the condition = %+v; want True naming %s", got, testInstallerDigest)
	}

	record(ichorv1alpha1.InstallerImageVerified)

	if got := read(); got == nil || got.Status != metav1.ConditionTrue {
		t.Fatalf("a later verified run left the condition %+v; want it still True", got)
	}
}
