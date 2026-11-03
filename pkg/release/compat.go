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
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/maeshinshin/ichor/pkg/upgrade"
)

// ErrUnknownVersion is wrapped by the compatibility errors when a version is not in any table: the
// answer is unknown, which is different from incompatible (adopt refuses the first and warns on the
// second).
var ErrUnknownVersion = errors.New("version not known")

// kubernetes133 is the lowest Kubernetes minor of the Talos 1.14 and Cilium 1.20 ranges below.
const kubernetes133 = "1.33"

// kubernetesRange is an inclusive range of Kubernetes minors, "1.33" to "1.37".
type kubernetesRange struct {
	min, max string
}

// talosKubernetes maps a Talos minor to the Kubernetes minors its official support matrix lists
// (https://docs.siderolabs.com/talos/<minor>/getting-started/support-matrix). When a Talos minor has
// an entry here the entry decides the pair, and machinery only bounds it: a pair the table lists
// must still pass upgrade.KubernetesPairing (ADR 0115 amendment 2026-11-03). Machinery decides the
// Talos minors the table lacks.
var talosKubernetes = map[string]kubernetesRange{
	// https://docs.siderolabs.com/talos/v1.14/getting-started/support-matrix lists Kubernetes
	// 1.33, 1.34, 1.35, 1.36 and 1.37. Machinery v1.14.5 accepts 1.32.0 to 1.37.99 for this minor.
	"1.14": {min: kubernetes133, max: "1.37"},
}

// ciliumKubernetes maps a Cilium minor to the Kubernetes minors its release notes list as e2e tested
// (the minors are all guaranteed compatible). Every Cilium minor is entered from its own page;
// a Cilium minor not here is unknown, and nothing is compatible with it.
var ciliumKubernetes = map[string]kubernetesRange{
	// https://docs.cilium.io/en/v1.18/network/kubernetes/requirements/ lists 1.30, 1.31, 1.32, 1.33.
	"1.18": {min: "1.30", max: kubernetes133},
	// https://docs.cilium.io/en/v1.19/network/kubernetes/requirements/ lists 1.31, 1.32, 1.33, 1.34.
	"1.19": {min: "1.31", max: "1.34"},
	// https://docs.cilium.io/en/v1.20/network/kubernetes/requirements/ lists 1.33, 1.34, 1.35, 1.36.
	"1.20": {min: kubernetes133, max: "1.36"},
}

// minorKey is the "X.Y" of a version written as "X.Y.Z" or "vX.Y.Z", or "" when it is not semver.
func minorKey(v string) string {
	return strings.TrimPrefix(semver.MajorMinor("v"+strings.TrimPrefix(v, "v")), "v")
}

// inRange reports whether the Kubernetes release k falls in r. An invalid k is in no range.
func inRange(k string, r kubernetesRange) bool {
	if !semver.IsValid(k) {
		return false
	}

	return semver.Compare(semver.MajorMinor(k), "v"+r.min) >= 0 && semver.Compare(semver.MajorMinor(k), "v"+r.max) <= 0
}

// TalosKubernetesError is nil when the Talos release supports the Kubernetes release (ADR 0115).
// A Talos minor with an entry in the typed table is decided by the table, and machinery must still
// accept the pair (it bounds the table). Machinery's compatibility table decides every other Talos
// minor it knows; any other Talos is not known, and not compatible.
func TalosKubernetesError(talos, kubernetes string) error {
	if !semver.IsValid(talos) || !semver.IsValid(kubernetes) {
		return fmt.Errorf("talos %q or kubernetes %q is not a version", talos, kubernetes)
	}

	pairErr := upgrade.KubernetesPairing(kubernetes, talos)

	if r, ok := talosKubernetes[minorKey(talos)]; ok {
		if !inRange(kubernetes, r) {
			return fmt.Errorf("talos %s supports kubernetes %s to %s, not %s", talos, r.min, r.max, kubernetes)
		}

		if pairErr != nil {
			return fmt.Errorf("talos %s with kubernetes %s is outside machinery's range: %w", talos, kubernetes, pairErr)
		}

		return nil
	}

	if pairErr == nil {
		return nil
	}

	if machineryKnows(talos) {
		return fmt.Errorf("talos %s does not support kubernetes %s: %w", talos, kubernetes, pairErr)
	}

	return fmt.Errorf("talos %s with kubernetes %s: not known, not compatible: %w: %w",
		talos, kubernetes, ErrUnknownVersion, pairErr)
}

// machineryKnows reports whether machinery's compatibility table has the Talos minor. For a minor it
// does not know, KubernetesPairing answers "is not supported"; for one it knows, every Kubernetes
// release below the table is "too old". v1.0.0 is below every table, so "too old" is the answer for a
// known minor. The message is machinery's, pinned by go.mod, and the test below checks it.
func machineryKnows(talos string) bool {
	err := upgrade.KubernetesPairing("v1.0.0", talos)

	return err != nil && strings.Contains(err.Error(), "too old")
}

// CiliumKubernetesError is nil when the Cilium minor is e2e tested with the Kubernetes release (ADR 0115).
// A Cilium minor the table does not know is an error wrapping ErrUnknownVersion: nothing is known to be
// compatible with it.
func CiliumKubernetesError(cilium, kubernetes string) error {
	if !semver.IsValid(kubernetes) {
		return fmt.Errorf("kubernetes %q is not a version", kubernetes)
	}

	r, ok := ciliumKubernetes[minorKey(cilium)]
	if !ok {
		return fmt.Errorf("cilium %s is not known, not compatible with kubernetes %s: %w",
			cilium, kubernetes, ErrUnknownVersion)
	}

	if !inRange(kubernetes, r) {
		return fmt.Errorf("cilium %s is tested with kubernetes %s to %s, not %s", cilium, r.min, r.max, kubernetes)
	}

	return nil
}
