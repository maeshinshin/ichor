package v1alpha1

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

const (
	somethingElse       = "something else"
	proxyReplaced       = "replaced"
	aPersonIsNotLimited = "a person is not limited"
	alice               = "alice"
)

// TestAutoPatchIdentityMayOnlyApplyAPatch: the automatic patcher's ServiceAccount has one write to a TalosCluster: a
// newer patch of the same minor in the two version fields, nothing else. The controller's own
// username has none.
func TestAutoPatchIdentityMayOnlyApplyAPatch(t *testing.T) {
	t.Parallel()

	const (
		controller = "system:serviceaccount:ichor-system:ichor-controller"
		patcher    = "system:serviceaccount:ichor-system:ichor-auto-patch"
	)

	base := func() *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Spec.TalosVersion, c.Spec.KubernetesVersion = testTalosVersion, "v1.36.1"

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
		{name: "another field", user: patcher, mod: func(c *ichorv1alpha1.TalosCluster) { c.Spec.VersionContract = "v1.14" }, wantErr: somethingElse},
		{name: "an annotation", user: patcher, mod: func(c *ichorv1alpha1.TalosCluster) { c.Annotations = map[string]string{"a": "b"} }, wantErr: somethingElse},
		{name: "the controller writes no spec, not even a patch", user: controller, mod: talos("v1.14.5"), wantErr: "never writes"},
		{name: "the controller changes no other spec field either", user: controller, mod: func(c *ichorv1alpha1.TalosCluster) { c.Spec.VersionContract = "v1.14" }, wantErr: "never writes"},
		{name: aPersonIsNotLimited, user: alice, mod: kube("v1.37.0")},
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

// TestStorageExtensionsIdentityMayOnlySetTheSchematicID: the ServiceAccount of ADR 0173 has one write to a
// TalosCluster: spec.schematic.id, from unset or the stock schematic to a non-empty ID, nothing else.
func TestStorageExtensionsIdentityMayOnlySetTheSchematicID(t *testing.T) {
	t.Parallel()

	const writer = "system:serviceaccount:ichor-system:ichor-storage-extensions"

	withSchematic := func(id string) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Spec.TalosVersion = testTalosVersion

		if id != "" {
			c.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: id}
		}

		return c
	}

	for _, tc := range []struct {
		name     string
		user     string
		old, new *ichorv1alpha1.TalosCluster
		wantErr  string
	}{
		{name: "from unset", user: writer, old: withSchematic(""), new: withSchematic("abc")},
		{name: "from the stock schematic", user: writer, old: withSchematic(upgrade.DefaultSchematic), new: withSchematic("abc")},
		{name: "over a custom schematic", user: writer, old: withSchematic("mine"), new: withSchematic("abc"), wantErr: "neither unset nor the stock"},
		{name: "to an empty ID", user: writer, old: withSchematic(upgrade.DefaultSchematic), new: withSchematic(""), wantErr: "ID is empty"},
		{name: "another field", user: writer, old: withSchematic(""), new: func() *ichorv1alpha1.TalosCluster {
			c := withSchematic("abc")
			c.Spec.TalosVersion = "v1.14.5"

			return c
		}(), wantErr: somethingElse},
		{name: aPersonIsNotLimited, user: alice, old: withSchematic("mine"), new: withSchematic("abc")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := &TalosClusterCustomValidator{StorageExtensionsUser: writer}

			err := v.limitController(settingsCtx(tc.user, false), tc.old, tc.new)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

// TestConsentSweepIdentityMayOnlyRemoveTheConsentAnnotation: the ServiceAccount of ADR 0066's 2026-11-22
// amendment has one write to a TalosCluster: removing the kube-proxy switch consent annotation, nothing else.
func TestConsentSweepIdentityMayOnlyRemoveTheConsentAnnotation(t *testing.T) {
	t.Parallel()

	const (
		sweeper = "system:serviceaccount:ichor-system:ichor-consent-sweep"
		consent = ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation
	)

	cluster := func(annotations map[string]string) *ichorv1alpha1.TalosCluster {
		c := &ichorv1alpha1.TalosCluster{}
		c.Spec.TalosVersion = testTalosVersion
		c.Annotations = annotations

		return c
	}

	for _, tc := range []struct {
		name     string
		user     string
		old, new *ichorv1alpha1.TalosCluster
		wantErr  string
	}{
		{name: "the consent removed", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced}), new: cluster(nil)},
		{name: "the consent removed, another annotation kept", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced, "a": "b"}), new: cluster(map[string]string{"a": "b"})},
		{name: "no change", user: sweeper, old: cluster(nil), new: cluster(nil)},
		{name: "the consent added", user: sweeper,
			old: cluster(nil), new: cluster(map[string]string{consent: proxyReplaced}), wantErr: somethingElse},
		{name: "the consent rewritten", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced}), new: cluster(map[string]string{consent: "enabled"}), wantErr: somethingElse},
		{name: "another annotation removed", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced, "a": "b"}), new: cluster(map[string]string{consent: proxyReplaced}), wantErr: somethingElse},
		{name: "another annotation added with the removal", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced}), new: cluster(map[string]string{"a": "b"}), wantErr: somethingElse},
		{name: "a spec field with the removal", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced}), new: func() *ichorv1alpha1.TalosCluster {
				c := cluster(nil)
				c.Spec.TalosVersion = "v1.14.5"

				return c
			}(), wantErr: somethingElse},
		{name: "a label with the removal", user: sweeper,
			old: cluster(map[string]string{consent: proxyReplaced}), new: func() *ichorv1alpha1.TalosCluster {
				c := cluster(nil)
				c.Labels = map[string]string{"a": "b"}

				return c
			}(), wantErr: somethingElse},
		{name: aPersonIsNotLimited, user: alice,
			old: cluster(nil), new: cluster(map[string]string{consent: proxyReplaced})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			v := &TalosClusterCustomValidator{ConsentSweepUser: sweeper}

			err := v.limitController(settingsCtx(tc.user, false), tc.old, tc.new)

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
