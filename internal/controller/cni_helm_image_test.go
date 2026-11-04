package controller

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// The Cilium Helm Job runs the installer image the last Ichor upgrade recorded, by digest, when that record
// is for the installed version and repository; otherwise the tag, as before.
func TestCNIHelmJobImageUsesTheRecordedInstallerDigest(t *testing.T) {
	const (
		digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		mirror = "mirror.example/ichor-installer"
	)

	repo := cniTestInstallation().Spec.InstallerRepository

	record := func(version, repository, digest string) *ichorv1alpha1.IchorInstallation {
		inst := cniTestInstallation()
		inst.Status.InstallerImage = &ichorv1alpha1.IchorInstallerImageStatus{Version: version, Repository: repository, Digest: digest}

		return inst
	}

	for name, tc := range map[string]struct {
		inst *ichorv1alpha1.IchorInstallation
		want string
	}{
		"no record":                 {cniTestInstallation(), repo + ":v0.9.0"},
		"record for this version":   {record("v0.9.0", repo, digest), repo + "@" + digest},
		"record of another version": {record("v0.8.0", repo, digest), repo + ":v0.9.0"},
		"record of another repo":    {record("v0.9.0", mirror, digest), repo + ":v0.9.0"},
		"record without a digest":   {record("v0.9.0", repo, ""), repo + ":v0.9.0"},
	} {
		t.Run(name, func(t *testing.T) {
			job := newCNIHelmJob("ichor-system", "j", cniTestCluster(""), cni.KubeProxyEnabled, tc.inst, "h", false, 0)
			if got := job.Spec.Template.Spec.Containers[0].Image; got != tc.want {
				t.Fatalf("image = %q, want %q", got, tc.want)
			}
		})
	}
}
