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

// Package release is the one set of versions an Ichor build supports (ADR 0113): the Talos release
// and its version contract, the Kubernetes release and the Cilium chart. Init, adopt, the CLI, the
// API, the admission webhook and the controller all read Pinned; no other code names these
// versions. Choosing another version means running another Ichor build.
//
// The functions here are pure: they take version strings and return answers. They do not talk to a
// cluster.
package release

import (
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/maeshinshin/ichor/pkg/cni/cilium"
)

// Set is one supported combination of versions.
type Set struct {
	// Talos is the Talos release, "vX.Y.Z".
	Talos string
	// VersionContract is the Talos version contract the configuration is rendered with, "vX.Y".
	VersionContract string
	// Kubernetes is the Kubernetes release, "vX.Y.Z". Talos 1.14.3 supports 1.32.0 to 1.37.99.
	Kubernetes string
	// Cilium is the Cilium chart version, without the "v" (the chart's own version string).
	Cilium string
}

// Pinned is the set this Ichor build supports. Its Talos release and Kubernetes release agree with
// pkg/machinery in go.mod (the Talos 1.14.3 compatibility table; the test in this package checks it).
var Pinned = Set{
	Talos:           "v1.14.3",
	VersionContract: "v1.14",
	Kubernetes:      "v1.37.1",
	Cilium:          cilium.ChartVersion,
}

// State is how a component of a cluster compares with the pinned set.
type State string

// The states a component can be in.
const (
	// UpToDate means the cluster runs the pinned version.
	UpToDate State = "up-to-date"
	// Behind means the cluster runs an older version; a rollout brings it to the pinned one.
	Behind State = "behind"
	// Ahead means the cluster runs a newer version than this build supports. Nothing Ichor does
	// moves it back; the admission rules refuse to create or keep such a cluster.
	Ahead State = "ahead"
	// Unknown means the current version is not known (for example Cilium before it is observed).
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

// CheckCreate is the admission rule for a version field of a new TalosCluster (ADR 0113): the value
// must be a semantic version no newer than the pinned one. An adopted cluster is allowed to be
// behind; `ichor init` always writes the pinned value.
func CheckCreate(field, value, pinned string) error {
	if !semver.IsValid(value) {
		return fmt.Errorf("spec.%s %q is not a version", field, value)
	}

	if Compare(value, pinned) == Ahead {
		return fmt.Errorf("spec.%s %s is newer than %s, the version this Ichor build supports (ADR 0113): "+
			"use the Ichor release that supports it", field, value, pinned)
	}

	return nil
}

// CheckUpdate is the admission rule for a version field that an update changes (ADR 0113): the new
// value must be the pinned one, reached without a downgrade. An unchanged value is always allowed,
// so a stored object that is behind can still be edited for something else.
func CheckUpdate(field, oldValue, newValue, pinned string) error {
	if oldValue == newValue {
		return nil
	}

	if newValue != pinned {
		return fmt.Errorf("spec.%s may only move to %s, the version this Ichor build supports (ADR 0113); %s is not that version",
			field, pinned, newValue)
	}

	if semver.IsValid(oldValue) && semver.Compare(newValue, oldValue) < 0 {
		return fmt.Errorf("spec.%s cannot downgrade from %s to %s", field, oldValue, newValue)
	}

	return nil
}

// CheckTarget refuses an upgrade target that is not the pinned version (ADR 0113). kind names the
// component for the message ("Talos", "Kubernetes").
func CheckTarget(kind, target, pinned string) error {
	if target == pinned {
		return nil
	}

	return fmt.Errorf("%s %s is not the version this Ichor build supports, %s (ADR 0113): "+
		"an upgrade moves the cluster to the release set of its Ichor, see `ichor upgrade cluster`", kind, target, pinned)
}

// Adoptable judges the versions of a cluster adopt is about to bring under management. Older than
// the pinned set is allowed and reported in behind; newer is refused (ADR 0113).
func Adoptable(talos, kubernetes string) (behind []string, err error) {
	if talos != "" {
		switch Compare(talos, Pinned.Talos) {
		case Ahead:
			return nil, fmt.Errorf("the cluster runs Talos %s, newer than %s, the version this Ichor build supports: "+
				"adopt needs an Ichor release that supports it (ADR 0113)", talos, Pinned.Talos)
		case Behind:
			behind = append(behind, fmt.Sprintf("Talos %s (supported: %s)", talos, Pinned.Talos))
		}
	}

	if kubernetes != "" {
		switch Compare(kubernetes, Pinned.Kubernetes) {
		case Ahead:
			return nil, fmt.Errorf("the cluster runs Kubernetes %s, newer than %s, the version this Ichor build supports (ADR 0113)",
				kubernetes, Pinned.Kubernetes)
		case Behind:
			behind = append(behind, fmt.Sprintf("Kubernetes %s (supported: %s)", kubernetes, Pinned.Kubernetes))
		}
	}

	return behind, nil
}

// Step is one step of a rollout that brings a cluster to the pinned set (ADR 0113). A rollout runs
// them in this order.
type Step string

// The rollout's steps, in the order they run.
const (
	// StepTalos upgrades Talos on every machine and writes the pinned version contract with it.
	// Talos goes first: Kubernetes is only supported on the Talos that runs it (ADR 0057), and the
	// Talos upgrade docs say Talos and Kubernetes upgrade separately.
	StepTalos Step = "talos"
	// StepConfig applies the configuration the new contract changes. It runs only when Talos is
	// already at the pinned version, because the Kubernetes step refuses while a machine has a
	// pending change it does not apply.
	StepConfig Step = "config"
	// StepKubernetes upgrades Kubernetes (ADR 0087). It needs the running Talos to support the
	// pinned Kubernetes, which StepTalos establishes.
	StepKubernetes Step = "kubernetes"
	// StepCilium upgrades the Cilium release to the embedded chart (ADR 0099). It runs last, so the
	// new chart meets the Kubernetes the set was tested with.
	StepCilium Step = "cilium"
)

// Running is what a cluster runs, as a rollout needs to know it.
type Running struct {
	Talos           string
	VersionContract string
	Kubernetes      string
	// CiliumBehind is true when the Cilium release is older than the embedded chart
	// (status.cni.availableChartVersion is set).
	CiliumBehind bool
	// ConfigPending is true when an approved machine has a configuration change that needs a reboot
	// or an API server restart.
	ConfigPending bool
}

// Steps returns the steps that bring a cluster in state r to the pinned set, in order. It is empty
// when the cluster runs the set. A rollout re-plans after each step, so the list is for the state
// now: a Talos step can leave configuration pending that the config step then applies.
func Steps(r Running) []Step {
	talosBehind := Compare(r.Talos, Pinned.Talos) == Behind || Compare(r.VersionContract, Pinned.VersionContract) == Behind
	k8sBehind := Compare(r.Kubernetes, Pinned.Kubernetes) == Behind

	var out []Step

	if talosBehind {
		out = append(out, StepTalos)
	}

	if r.ConfigPending && !talosBehind && k8sBehind {
		out = append(out, StepConfig)
	}

	if k8sBehind {
		out = append(out, StepKubernetes)
	}

	if r.CiliumBehind {
		out = append(out, StepCilium)
	}

	return out
}
