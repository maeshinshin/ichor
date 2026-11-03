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

// versionsOf is the versions of a group, for comparing a grouping with the expected list.
func versionsOf(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Version)
	}

	return out
}

func TestTalosChoicesGroupPatchesOfTheMinorAndTheNextMinorOnly(t *testing.T) {
	// From 1.12.0: 1.15.0 is unknown to this machinery, 1.14.x skips a minor and is not offered.
	c := TalosChoices("v1.12.0", "v1.14.3", fakeTalosTags)

	if got := versionsOf(c.Recommended); !slices.Equal(got, []string{"v1.14.3"}) {
		t.Errorf("Recommended = %v", got)
	}

	if got := versionsOf(c.Patch); !slices.Equal(got, []string{"v1.12.4"}) {
		t.Errorf("Patch = %v", got)
	}

	if got := versionsOf(c.NextMinor); !slices.Equal(got, []string{"v1.13.2"}) {
		t.Errorf("NextMinor = %v", got)
	}

	if got := c.Default(); got != "v1.14.3" {
		t.Errorf("Default = %q, want the recommended one", got)
	}
}

func TestTalosNextMinorBoundaryAndNoRepeatOfTheRecommended(t *testing.T) {
	// From 1.13.2 the next minor is 1.14: its newest patches are listed newest first, and the
	// recommended 1.14.3 is not repeated among them.
	c := TalosChoices("v1.13.2", "v1.14.3", fakeTalosTags)

	if got := versionsOf(c.Recommended); !slices.Equal(got, []string{"v1.14.3"}) {
		t.Errorf("Recommended = %v", got)
	}

	if got := versionsOf(c.NextMinor); !slices.Equal(got, []string{"v1.14.5", "v1.14.4"}) {
		t.Errorf("NextMinor = %v", got)
	}

	if len(c.Patch) != 0 {
		t.Errorf("Patch = %v, want none: 1.13.2 is the newest 1.13", versionsOf(c.Patch))
	}
}

func TestTalosDefaultWithoutTheRecommended(t *testing.T) {
	// The release set's version is not newer than running: the default is the newest patch.
	c := TalosChoices("v1.14.3", "v1.14.3", fakeTalosTags)

	if len(c.Recommended) != 0 {
		t.Errorf("Recommended = %v, want none", versionsOf(c.Recommended))
	}

	if got := c.Default(); got != "v1.14.5" {
		t.Errorf("Default = %q, want v1.14.5", got)
	}

	// The pinned version is not in the tag list: nothing is recommended, and with no patch the
	// default is empty.
	c = TalosChoices("v1.13.2", "v1.14.9", fakeTalosTags)
	if c.Default() != "" {
		t.Errorf("Default = %q, want empty", c.Default())
	}
}

func TestKubernetesChoicesPairWithTalosAndStepOneMinor(t *testing.T) {
	// Talos 1.14.3 supports 1.32.0 to 1.37.99. 1.38.0 skips a minor.
	c := KubernetesChoices("v1.36.0", "v1.14.3", "v1.37.1", fakeKubernetesTags)

	if got := versionsOf(c.Recommended); !slices.Equal(got, []string{"v1.37.1"}) {
		t.Errorf("Recommended = %v", got)
	}

	if got := versionsOf(c.Patch); !slices.Equal(got, []string{"v1.36.1"}) {
		t.Errorf("Patch = %v", got)
	}

	if got := versionsOf(c.NextMinor); !slices.Equal(got, []string{"v1.37.2", "v1.37.0"}) {
		t.Errorf("NextMinor = %v", got)
	}

	if got := c.Default(); got != "v1.37.1" {
		t.Errorf("Default = %q", got)
	}
}

func TestKubernetesPairingFiltersTheNextMinor(t *testing.T) {
	// Talos 1.12 supports 1.30.0 to 1.35.99: 1.36 is one minor up but the pairing refuses it, and
	// the recommended 1.37.1 is neither a valid skew nor paired.
	c := KubernetesChoices("v1.34.2", "v1.12.0", "v1.37.1", fakeKubernetesTags)

	if len(c.Recommended) != 0 {
		t.Errorf("Recommended = %v, want none", versionsOf(c.Recommended))
	}

	if got := versionsOf(c.Patch); !slices.Equal(got, []string{"v1.34.3"}) {
		t.Errorf("Patch = %v", got)
	}

	if got := versionsOf(c.NextMinor); !slices.Equal(got, []string{"v1.35.0"}) {
		t.Errorf("NextMinor = %v", got)
	}

	c = KubernetesChoices("v1.35.0", "v1.12.0", "v1.37.1", fakeKubernetesTags)
	if len(c.Patch)+len(c.NextMinor)+len(c.Recommended) != 0 {
		t.Errorf("the pairing refuses 1.36 from Talos 1.12: got %+v", c)
	}
}

func TestKubernetesCompatibilityNote(t *testing.T) {
	cases := []struct {
		name, kubernetes, chart, want string
	}{
		{"within the tested range", "v1.33.5", "1.18.0", ""},
		{"one minor above the tested range", "v1.34.0", "1.18.0", "Cilium 1.18 is tested with Kubernetes up to 1.33"},
		{"no entry for the Cilium minor", "v1.37.1", "1.19.0", ""},
		{"not semver", "latest", "1.18.0", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ciliumNote(tc.kubernetes, tc.chart); got != tc.want {
				t.Fatalf("ciliumNote(%q, %q) = %q, want %q", tc.kubernetes, tc.chart, got, tc.want)
			}
		})
	}

	// The embedded chart is the one the table is written for: a Kubernetes release above 1.33 is
	// noted in the list, and the note never removes the candidate.
	c := KubernetesChoices("v1.36.0", "v1.14.3", "v1.37.1", fakeKubernetesTags)
	if c.Recommended[0].Note == "" || c.NextMinor[0].Note == "" {
		t.Errorf("the candidates above 1.33 carry no note: %+v", c)
	}
}

func TestDefaultIsEmptyWithoutAnyOffer(t *testing.T) {
	if got := (Choices{}).Default(); got != "" {
		t.Fatalf("Default = %q", got)
	}
}

func TestAtLeastKeepsTheFloorAndTheOrder(t *testing.T) {
	got := AtLeast([]string{"v1.37.1", "v1.29.9", "v1.30.0", "junk"}, KubernetesFloor)
	if want := []string{"v1.37.1", "v1.30.0"}; !slices.Equal(got, want) {
		t.Fatalf("AtLeast = %v, want %v", got, want)
	}
}
