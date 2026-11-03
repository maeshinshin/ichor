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

package release

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// The lowest releases an upgrade lists. Talos 1.12 is the adopt floor.
// Kubernetes 1.30.0 is the lowest release machinery pairs with Talos 1.12.
const (
	TalosFloor      = "v1.12.0"
	KubernetesFloor = "v1.30.0"
)

// ciliumMaxKubernetes is the highest Kubernetes minor each Cilium minor is tested with, keyed by the
// Cilium minor without its v. Source: https://docs.cilium.io/en/v1.18/network/kubernetes/requirements/
// (read 2026-11-03), which lists 1.30, 1.31, 1.32 and 1.33 as e2e tested for Cilium 1.18 and says
// nothing about newer releases. A Cilium minor missing here gets no note.
var ciliumMaxKubernetes = map[string]string{
	"1.18": "1.33",
}

// Candidate is a version an upgrade can move to, with a note that never blocks the choice ("" when
// there is none).
type Candidate struct {
	Version string
	Note    string
}

// Choices is the upgrade list of one component in the groups the UI and CLI show.
type Choices struct {
	// Recommended is the release set's version, when it is newer than running and a valid upgrade.
	// It is at most one entry, and it is never repeated in the other groups.
	Recommended []Candidate
	// Patch is the newer releases of the running minor, newest first.
	Patch []Candidate
	// NextMinor is the releases of the minor after the running one, newest first. Talos skips no
	// minor in the offer, and Kubernetes is one minor up at most (upgrade.KubernetesSkew).
	NextMinor []Candidate
}

// Default is the version an upgrade proposes when the user names none: the recommended one, else the
// newest patch of the running minor, else "".
func (c Choices) Default() string {
	switch {
	case len(c.Recommended) > 0:
		return c.Recommended[0].Version
	case len(c.Patch) > 0:
		return c.Patch[0].Version
	default:
		return ""
	}
}

// Choose groups the final releases in tags for an upgrade from running. valid reports whether moving
// from running to a tag is a valid upgrade; note gives the note of a candidate. pinned is the release
// set's version for the component. A running version that is not semver offers nothing.
func Choose(running, pinned string, tags []string, valid func(to string) bool, note func(to string) string) Choices {
	var out Choices

	if !semver.IsValid(running) {
		return out
	}

	candidates := releasecheck.Finals(tags)
	offerable := func(v string) bool {
		return semver.Compare(v, running) > 0 && valid(v)
	}

	rec := ""
	if slices.Contains(candidates, pinned) && offerable(pinned) {
		rec = pinned
		out.Recommended = []Candidate{{Version: pinned, Note: note(pinned)}}
	}

	for _, v := range candidates {
		if v == rec || !offerable(v) {
			continue
		}

		c := Candidate{Version: v, Note: note(v)}

		switch {
		case semver.MajorMinor(v) == semver.MajorMinor(running):
			out.Patch = append(out.Patch, c)
		case semver.Major(v) == semver.Major(running) && minorOf(v) == minorOf(running)+1:
			out.NextMinor = append(out.NextMinor, c)
		}
	}

	return out
}

// minorOf is the minor number of a valid semantic version.
func minorOf(v string) int {
	_, m, _ := strings.Cut(strings.TrimPrefix(semver.MajorMinor(v), "v"), ".")
	n, _ := strconv.Atoi(m)

	return n
}

// TalosChoices groups the Talos releases an upgrade from running can move to (upgrade.TalosPath).
func TalosChoices(running, pinned string, tags []string) Choices {
	return Choose(running, pinned, tags, func(to string) bool {
		return upgrade.TalosPath(running, to) == nil
	}, func(string) string { return "" })
}

// KubernetesChoices groups the Kubernetes releases an upgrade from running can move to: at most one
// minor up (upgrade.KubernetesSkew) and supported by the Talos the cluster runs
// (upgrade.KubernetesPairing). A release the embedded Cilium is not tested with gets a note.
func KubernetesChoices(running, talosRunning, pinned string, tags []string) Choices {
	return Choose(running, pinned, tags, func(to string) bool {
		return upgrade.KubernetesSkew(running, to) == nil && upgrade.KubernetesPairing(to, talosRunning) == nil
	}, func(to string) string {
		return ciliumNote(to, cilium.ChartVersion)
	})
}

// ciliumNote is the note for a Kubernetes release under a Cilium chart: when the table says the
// Cilium minor is tested only up to a lower Kubernetes minor, it names that minor.
func ciliumNote(kubernetes, chart string) string {
	cm := strings.TrimPrefix(semver.MajorMinor("v"+strings.TrimPrefix(chart, "v")), "v")

	maxK, ok := ciliumMaxKubernetes[cm]
	if !ok || !semver.IsValid(kubernetes) {
		return ""
	}

	if semver.Compare(semver.MajorMinor(kubernetes), "v"+maxK) > 0 {
		return fmt.Sprintf("Cilium %s is tested with Kubernetes up to %s", cm, maxK)
	}

	return ""
}

// AtLeast keeps the versions that are not older than floor, in their order.
func AtLeast(versions []string, floor string) []string {
	var out []string

	for _, v := range versions {
		if semver.IsValid(v) && semver.Compare(v, floor) >= 0 {
			out = append(out, v)
		}
	}

	return out
}
