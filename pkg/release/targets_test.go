//nolint:goconst // version tables repeat the same release names on purpose
package release

import (
	"slices"
	"testing"
)

// The tag lists are a registry's as the lister returns them: finals, prereleases, shorthands and
// junk, in no order.
var fakeTalosTags = []string{
	"v1.11.5", "v1.12.0", "v1.12.4", "v1.13.2", "v1.14.3", "v1.14.4-rc.1", "v1.14.4",
	"v1.15.0", "1.14.5", "latest", "v1.14", "v1.14.4+build.1",
}

var fakeKubernetesTags = []string{
	"v1.34.2", "v1.34.3", "v1.35.0", "v1.36.0", "v1.36.1", "v1.37.0", "v1.37.1",
	"v1.38.0", "v1.37.2-rc.1", "v1.37.2", "sha-1234",
}

func TestTalosChoicesGroupPatchesOfTheMinorAndTheNextMinorOnly(t *testing.T) {
	// From 1.12.0 on Kubernetes 1.33.0: 1.15.0 is unknown to this machinery, and 1.14.x skips a minor
	// and is not offered, except the recommended 1.14.3, which the release set names.
	c := TalosChoices("v1.12.0", "v1.33.0", "v1.14.3", fakeTalosTags)

	if !slices.Equal(c.Recommended, []string{"v1.14.3"}) {
		t.Errorf("Recommended = %v", c.Recommended)
	}

	if !slices.Equal(c.Patch, []string{"v1.12.4"}) {
		t.Errorf("Patch = %v", c.Patch)
	}

	if !slices.Equal(c.NextMinor, []string{"v1.13.2"}) {
		t.Errorf("NextMinor = %v", c.NextMinor)
	}

	if got := c.Default(); got != "v1.14.3" {
		t.Errorf("Default = %q, want the recommended one", got)
	}
}

func TestTalosChoicesRequireTheRunningKubernetesToBeSupported(t *testing.T) {
	// Kubernetes 1.36.0 is outside Talos 1.12's range (1.30.0 to 1.35.99), so the 1.12 patch is not
	// offered; Talos 1.13 and 1.14 support it.
	c := TalosChoices("v1.12.0", "v1.36.0", "v1.14.3", fakeTalosTags)

	if len(c.Patch) != 0 {
		t.Errorf("Patch = %v, want none", c.Patch)
	}

	if !slices.Equal(c.NextMinor, []string{"v1.13.2"}) {
		t.Errorf("NextMinor = %v", c.NextMinor)
	}
}

func TestTalosNextMinorBoundaryAndNoRepeatOfTheRecommended(t *testing.T) {
	// From 1.13.2 the next minor is 1.14: its newest patches are listed newest first, and the
	// recommended 1.14.3 is not repeated among them.
	c := TalosChoices("v1.13.2", "v1.33.0", "v1.14.3", fakeTalosTags)

	if !slices.Equal(c.Recommended, []string{"v1.14.3"}) {
		t.Errorf("Recommended = %v", c.Recommended)
	}

	if !slices.Equal(c.NextMinor, []string{"v1.14.5", "v1.14.4"}) {
		t.Errorf("NextMinor = %v", c.NextMinor)
	}

	if len(c.Patch) != 0 {
		t.Errorf("Patch = %v, want none: 1.13.2 is the newest 1.13", c.Patch)
	}
}

func TestTalosDefaultSelection(t *testing.T) {
	// The release set's version is not newer than running: the default is the newest patch.
	c := TalosChoices("v1.14.3", "v1.33.0", "v1.14.3", fakeTalosTags)

	if len(c.Recommended) != 0 {
		t.Errorf("Recommended = %v, want none", c.Recommended)
	}

	if got := c.Default(); got != "v1.14.5" {
		t.Errorf("Default = %q, want v1.14.5", got)
	}

	// The release set's version is a patch: it is recommended, and not repeated in the patch group.
	c = TalosChoices("v1.14.3", "v1.33.0", "v1.14.4", fakeTalosTags)

	if !slices.Equal(c.Recommended, []string{"v1.14.4"}) || !slices.Equal(c.Patch, []string{"v1.14.5"}) {
		t.Errorf("Recommended = %v, Patch = %v", c.Recommended, c.Patch)
	}

	if got := c.Default(); got != "v1.14.4" {
		t.Errorf("Default = %q, want the recommended v1.14.4", got)
	}

	// The release set's version is not in the tag list: nothing is recommended, and with no patch
	// the default is empty.
	c = TalosChoices("v1.13.2", "v1.33.0", "v1.14.9", fakeTalosTags)
	if c.Default() != "" {
		t.Errorf("Default = %q, want empty", c.Default())
	}
}

func TestKubernetesChoicesNeedCiliumAndTalosToSupportThem(t *testing.T) {
	// Cilium 1.18 is tested with Kubernetes 1.30 to 1.33: from 1.36.0 nothing is offered, and the
	// release set's 1.37.1 is not tested with it either.
	c := KubernetesChoices("v1.36.0", "v1.14.3", "1.18.0", "v1.37.1", true, fakeKubernetesTags)
	if c.Default() != "" || len(c.Patch)+len(c.NextMinor)+len(c.Recommended) != 0 {
		t.Errorf("Cilium 1.18 offers %+v from 1.36.0", c)
	}

	// Cilium 1.20 is tested with 1.33 to 1.36: 1.36.1 is offered as a patch, and the release set's
	// 1.37.1 is not (not tested with Cilium 1.20, and one minor up from 1.36.0 is still allowed).
	c = KubernetesChoices("v1.36.0", "v1.14.3", "1.20.0", "v1.37.1", true, fakeKubernetesTags)

	if !slices.Equal(c.Patch, []string{"v1.36.1"}) {
		t.Errorf("Patch = %v", c.Patch)
	}

	if len(c.Recommended) != 0 || len(c.NextMinor) != 0 {
		t.Errorf("Recommended = %v, NextMinor = %v, want none", c.Recommended, c.NextMinor)
	}

	if got := c.Default(); got != "v1.36.1" {
		t.Errorf("Default = %q", got)
	}
}

func TestKubernetesNextMinorBoundaryAndPairing(t *testing.T) {
	// From 1.34.2 on Talos 1.14.3 with Cilium 1.20 (1.33 to 1.36): 1.35.0 is the next minor, 1.36.x
	// skips one (refused by the skew), and 1.37.1 is refused by the skew and by Cilium.
	c := KubernetesChoices("v1.34.2", "v1.14.3", "1.20.0", "v1.37.1", true, fakeKubernetesTags)

	if !slices.Equal(c.Patch, []string{"v1.34.3"}) {
		t.Errorf("Patch = %v", c.Patch)
	}

	if !slices.Equal(c.NextMinor, []string{"v1.35.0"}) {
		t.Errorf("NextMinor = %v", c.NextMinor)
	}

	if got := c.Default(); got != "v1.34.3" {
		t.Errorf("Default = %q", got)
	}

	// Talos 1.12 supports 1.30.0 to 1.35.99: from 1.35.0, 1.36.x is one minor up but the Talos
	// pairing refuses it, so nothing is offered.
	c = KubernetesChoices("v1.35.0", "v1.12.0", "1.20.0", "v1.37.1", true, fakeKubernetesTags)
	if len(c.Patch)+len(c.NextMinor)+len(c.Recommended) != 0 {
		t.Errorf("the Talos pairing should refuse 1.36 from Talos 1.12: %+v", c)
	}
}

func TestKubernetesUnknownCiliumOffersNothing(t *testing.T) {
	c := KubernetesChoices("v1.34.2", "v1.14.3", "1.21.0", "v1.37.1", true, fakeKubernetesTags)

	if len(c.Patch)+len(c.NextMinor)+len(c.Recommended) != 0 {
		t.Errorf("an unknown Cilium offers %+v", c)
	}
}

func TestKubernetesChoicesSkipTheCiliumConstraintOfANonCiliumCNI(t *testing.T) {
	// An observed non-Cilium CNI has no Cilium release to match (ADR 0115, amendment 3), so the Talos
	// pairing alone decides: the same pair that offers nothing under an unknown Cilium offers releases here.
	c := KubernetesChoices("v1.34.2", "v1.14.3", "", "v1.37.1", false, fakeKubernetesTags)

	if len(c.Patch)+len(c.NextMinor)+len(c.Recommended) == 0 {
		t.Errorf("a flannel cluster is offered no Kubernetes release: %+v", c)
	}
}

func TestAtLeastKeepsTheFloorAndTheOrder(t *testing.T) {
	got := AtLeast([]string{"v1.37.1", "v1.29.9", "v1.30.0", "junk"}, KubernetesFloor)
	if want := []string{"v1.37.1", "v1.30.0"}; !slices.Equal(got, want) {
		t.Fatalf("AtLeast = %v, want %v", got, want)
	}
}

func TestDefaultIsEmptyWithoutAnyOffer(t *testing.T) {
	if got := (Choices{}).Default(); got != "" {
		t.Fatalf("Default = %q", got)
	}
}
