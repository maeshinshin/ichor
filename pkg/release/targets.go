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
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/maeshinshin/ichor/internal/releasecheck"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// The lowest releases an upgrade lists. Talos 1.12 is the adopt floor.
// Kubernetes 1.30.0 is the lowest release machinery pairs with Talos 1.12.
const (
	TalosFloor      = "v1.12.0"
	KubernetesFloor = "v1.30.0"
)

// Choices is the upgrade list of one component in the groups the UI and CLI show. Every
// version in it is a valid upgrade of the cluster as it runs now (see TalosChoices, KubernetesChoices).
type Choices struct {
	// Recommended is the release set's version, when it is a valid upgrade. It is at most one
	// version, and it is never repeated in the other groups.
	Recommended []string
	// Patch is the newer releases of the running minor, newest first.
	Patch []string
	// NextMinor is the releases of the minor after the running one, newest first. Talos may skip a
	// minor when the path allows it, but the offer is only the next minor; Kubernetes is at most one
	// minor up (upgrade.KubernetesSkew).
	NextMinor []string
}

// Default is the version an upgrade proposes when the user names none: the recommended one, else the
// newest patch of the running minor, else "".
func (c Choices) Default() string {
	switch {
	case len(c.Recommended) > 0:
		return c.Recommended[0]
	case len(c.Patch) > 0:
		return c.Patch[0]
	default:
		return ""
	}
}

// Choose groups the final releases in tags for an upgrade from running. valid reports whether moving
// from running to a tag is a valid upgrade for the cluster as a whole; pinned is the release set's
// version for the component. A running version that is not semver offers nothing.
func Choose(running, pinned string, tags []string, valid func(to string) bool) Choices {
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
		out.Recommended = []string{pinned}
	}

	for _, v := range candidates {
		if v == rec || !offerable(v) {
			continue
		}

		switch {
		case semver.MajorMinor(v) == semver.MajorMinor(running):
			out.Patch = append(out.Patch, v)
		case semver.Major(v) == semver.Major(running) && minorOf(v) == minorOf(running)+1:
			out.NextMinor = append(out.NextMinor, v)
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

// TalosChoices groups the Talos releases an upgrade from running can move to. The Talos path must
// be accepted (upgrade.TalosPath), and the Talos release must support the Kubernetes the cluster
// runs (TalosKubernetesError).
func TalosChoices(running, kubernetesRunning, pinned string, tags []string) Choices {
	return Choose(running, pinned, tags, func(to string) bool {
		return upgrade.TalosPath(running, to) == nil && TalosKubernetesError(to, kubernetesRunning) == nil
	})
}

// KubernetesChoices groups the Kubernetes releases an upgrade from running can move to: at most one
// minor up (upgrade.KubernetesSkew), supported by the Talos the cluster runs, and listed as compatible with
// the Cilium the cluster runs (ciliumRunning, the version without its v). An unknown Cilium gives no
// Kubernetes release, as nothing is known to be compatible with it. ciliumConstrains is false when
// the observed CNI is not Cilium: then there is no Cilium to match and the
// Talos pairing alone decides.
func KubernetesChoices(
	running, talosRunning, ciliumRunning, pinned string, ciliumConstrains bool, tags []string,
) Choices {
	return Choose(running, pinned, tags, func(to string) bool {
		return upgrade.KubernetesSkew(running, to) == nil &&
			TalosKubernetesError(talosRunning, to) == nil &&
			(!ciliumConstrains || CiliumKubernetesError(ciliumRunning, to) == nil)
	})
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
