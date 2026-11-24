package controller

import (
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The installer's CiliumNetworkPolicy selects its pod by app.kubernetes.io/name (internal/installmanifest/network-policies.yaml),
// and the namespace denies everything else: a check Job under another label cannot reach the API server.
func TestInstallerCheckJobPodCarriesTheInstallerPolicyLabel(t *testing.T) {
	inst := &ichorv1alpha1.IchorInstallation{Spec: ichorv1alpha1.IchorInstallationSpec{Version: "v0.3.0", InstallerRepository: "r.example/ichor"}}
	check := NewInstallerCheckJob("ichor-system", "c", inst, "sha256:x", "", false)
	run := newInstallerJob("ichor-system", "r", &ichorv1alpha1.TalosOperation{}, inst, "sha256:x", "", false)

	if got, want := check.Spec.Template.Labels[nameLabel], run.Spec.Template.Labels[nameLabel]; got != want {
		t.Errorf("the check Job's pod is labelled %q, the installer's policy selects %q", got, want)
	}
}
