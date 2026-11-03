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

// Package release holds the one set of versions an Ichor build is tested with (ADR 0113, amended by
// ADR 0115): the Talos release and its version contract, the Kubernetes release and the Cilium chart.
// Pinned is what a first install gets: `ichor init`, the CLI defaults and the UI's proposed values
// read it. It does not bound an upgrade. After the install each of Talos, Kubernetes and Cilium
// upgrades on its own, and the rules an upgrade target must meet are in this package too
// (CheckTalosTarget, CheckKubernetesTarget).
//
// The functions here are pure: they take version strings and return answers. They do not talk to a
// cluster.
package release

import (
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// Set is one combination of versions.
type Set struct {
	// Talos is the Talos release, "vX.Y.Z".
	Talos string
	// VersionContract is the Talos version contract the configuration is rendered with, "vX.Y".
	VersionContract string
	// Kubernetes is the Kubernetes release, "vX.Y.Z". Talos 1.14 supports Kubernetes 1.33 to 1.37
	// (the Talos v1.14 support matrix).
	Kubernetes string
	// Cilium is the Cilium chart version, without the "v" (the chart's own version string).
	Cilium string
}

// Pinned is the set this Ichor build is tested with, and the one a first install gets. Its Talos
// release and Kubernetes release agree with pkg/machinery in go.mod (the test in this package checks
// it).
var Pinned = Set{
	Talos:           "v1.14.5",
	VersionContract: "v1.14",
	Kubernetes:      "v1.36.6",
	Cilium:          cilium.ChartVersion,
}

// State is how a version compares with a reference version.
type State string

// The states a version can be in.
const (
	// UpToDate means the two versions are the same.
	UpToDate State = "up-to-date"
	// Behind means the first is older than the reference.
	Behind State = "behind"
	// Ahead means the first is newer than the reference.
	Ahead State = "ahead"
	// Unknown means one of the versions is not known or not a semantic version.
	Unknown State = "unknown"
)

// Compare reports how current compares with pinned. Both are semantic versions ("vX.Y.Z" or the
// contract's "vX.Y"); anything else is Unknown.
func Compare(current, pinned string) State {
	if !semver.IsValid(current) || !semver.IsValid(pinned) {
		return Unknown
	}

	switch c := semver.Compare(current, pinned); {
	case c < 0:
		return Behind
	case c > 0:
		return Ahead
	default:
		return UpToDate
	}
}

// CheckUpdate is the admission rule for a version field that an update changes (ADR 0115): the new
// value must not be an older one. An unchanged value is always allowed, and so is a value that is
// not a semantic version (the webhook checks the form of a changed value before it gets here).
func CheckUpdate(field, oldValue, newValue string) error {
	if oldValue == newValue || !semver.IsValid(oldValue) || !semver.IsValid(newValue) {
		return nil
	}

	if semver.Compare(newValue, oldValue) < 0 {
		return fmt.Errorf("spec.%s cannot downgrade from %s to %s", field, oldValue, newValue)
	}

	return nil
}

// CheckTalosTarget reports whether an upgrade of Talos from running to target is one Ichor accepts
// (ADR 0115). The target must be a final "vX.Y.Z" newer than running, and upgrade.TalosPath must
// accept the step. TalosPath defers to pkg/machinery/compatibility: the target's minor release sets a
// minimum running version (1.12.0 for Talos 1.14) and a maximum (exclusive) for a downgrade, and
// denies listed versions. Within those bounds Talos may skip minor releases; its docs recommend
// adjacent ones (docs/adr/0115). Whether the Kubernetes the cluster runs is supported by the new
// Talos is checked by the Talos executor (ADR 0043, 0057), not here.
func CheckTalosTarget(running, target string) error {
	if err := checkNewerFinal(running, target); err != nil {
		return err
	}

	return upgrade.TalosPath(running, target)
}

// CheckKubernetesTarget reports whether an upgrade of Kubernetes from running to target is one Ichor
// accepts (ADR 0115). The target must be a final "vX.Y.Z" newer than running, upgrade.KubernetesSkew
// must accept the step (at most one minor release up, within the same major), and talos, the
// cluster's Talos, must support the target (upgrade.KubernetesPairing).
func CheckKubernetesTarget(running, target, talos string) error {
	if err := checkNewerFinal(running, target); err != nil {
		return err
	}

	if err := upgrade.KubernetesSkew(running, target); err != nil {
		return err
	}

	return upgrade.KubernetesPairing(target, talos)
}

// checkNewerFinal refuses a target that is not a final "vX.Y.Z" (no pre-release, no build metadata,
// and not the "vX.Y" shorthand) newer than running.
func checkNewerFinal(running, target string) error {
	if !semver.IsValid(running) {
		return fmt.Errorf("the running version %q is not a semantic version", running)
	}

	if !semver.IsValid(target) || semver.Canonical(target) != target || semver.Prerelease(target) != "" {
		return fmt.Errorf("the target %q is not a final version vX.Y.Z", target)
	}

	if semver.Compare(target, running) <= 0 {
		return fmt.Errorf("the target %s is not newer than %s", target, running)
	}

	return nil
}
