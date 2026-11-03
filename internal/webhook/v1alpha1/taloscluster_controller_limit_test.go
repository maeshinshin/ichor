package v1alpha1

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestAutoPatchIdentityMayOnlyApplyAPatch: the automatic patcher's ServiceAccount has one write to a TalosCluster
// (ADR 0116): a newer patch of the same minor in the two version fields, nothing else. The controller's own
// username has none.
func TestAutoPatchIdentityMayOnlyApplyAPatch(t *testing.T) {
	t.Parallel()

	const (
		controller = "system:serviceaccount:ichor-system:ichor-controller"
		patcher    = "system:serviceaccount:ichor-system:ichor-auto-patch"
	)

	base := func() *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Spec.TalosVersion, c.Spec.KubernetesVersion = "v1.14.3", "v1.36.1"

		return c
	}

	talos := func(v string) func(*ichorv1alpha1.TalosCluster) {
		return func(c *ichorv1alpha1.TalosCluster) { c.Spec.TalosVersion = v }
	}
	kube := func(v string) func(*ichorv1alpha1.TalosCluster) {
		return func(c *ichorv1alpha1.TalosCluster) { c.Spec.KubernetesVersion = v }
	}

	for _, tc := range []struct {
		name    string
		user    string
		mod     func(*ichorv1alpha1.TalosCluster)
		wantErr string
	}{
		{name: "a Talos patch", user: patcher, mod: talos("v1.14.5")},
		{name: "a Kubernetes patch", user: patcher, mod: kube("v1.36.6")},
		{name: "a minor", user: patcher, mod: kube("v1.37.0"), wantErr: "not a newer patch"},
		{name: "a downgrade", user: patcher, mod: talos("v1.14.2"), wantErr: "not a newer patch"},
		{name: "another field", user: patcher, mod: func(c *ichorv1alpha1.TalosCluster) { c.Spec.VersionContract = "v1.14" }, wantErr: "something else"},
		{name: "an annotation", user: patcher, mod: func(c *ichorv1alpha1.TalosCluster) { c.Annotations = map[string]string{"a": "b"} }, wantErr: "something else"},
		{name: "the controller writes no spec, not even a patch", user: controller, mod: talos("v1.14.5"), wantErr: "never writes"},
		{name: "the controller changes no other spec field either", user: controller, mod: func(c *ichorv1alpha1.TalosCluster) { c.Spec.VersionContract = "v1.14" }, wantErr: "never writes"},
		{name: "a person is not limited", user: "alice", mod: kube("v1.37.0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := &TalosClusterCustomValidator{ControllerUser: controller, AutoPatchUser: patcher}
			newObj := base()
			tc.mod(newObj)

			err := v.limitController(settingsCtx(tc.user, false), base(), newObj)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
