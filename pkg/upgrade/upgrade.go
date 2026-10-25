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

// Package upgrade answers the version questions design section "Upgrades"
// lists as pre-checks: is a Talos version upgrade path supported, is a
// Kubernetes version supported with a given Talos version, and does a
// Kubernetes version change respect Kubernetes' own skew policy. It is a
// pure package: every function here takes version strings and returns an
// error, nothing talks to a cluster. The other two pre-checks design
// section "Upgrades" lists -- etcd health, node readiness -- need a
// running cluster and are deliberately not declared here, not even as
// interfaces: Go interfaces belong to their consumer, declared where
// they're consumed, and the consumer (the upgrade state machine) does not
// exist yet (ADR 0022).
//
// Talos upgrade paths and the Talos/Kubernetes pairing are not
// reimplemented here: pkg/machinery/compatibility already carries exactly
// that knowledge, per Talos release, and is already a dependency (ADR
// 0003). Reusing talosctl's own upgrade-k8s logic
// (github.com/siderolabs/go-kubernetes/kubernetes/upgrade, a separate
// module from machinery) was evaluated and rejected for the Kubernetes
// skew check specifically -- see ADR 0022 for why.
package upgrade

import (
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/compatibility"
)

// TalosPath reports whether upgrading Talos from "from" to "to" (both
// "vX.Y.Z") is a path Talos itself supports. It defers entirely to
// pkg/machinery/compatibility's TalosVersion.UpgradeableFrom, which encodes
// the actual rule (a minimum host version to upgrade from, a maximum host
// version it can still downgrade to, and any explicitly denied versions)
// per Talos minor release -- not a generic "one minor at a time" rule,
// since Talos allows skipping minors within those bounds.
func TalosPath(from, to string) error {
	fromVersion, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: from})
	if err != nil {
		return fmt.Errorf("parsing current Talos version %q: %w", from, err)
	}

	toVersion, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: to})
	if err != nil {
		return fmt.Errorf("parsing target Talos version %q: %w", to, err)
	}

	return toVersion.UpgradeableFrom(fromVersion)
}

// KubernetesPairing reports whether kubernetesVersion ("vX.Y.Z") is one
// Talos talosVersion ("vX.Y.Z") supports running, per
// pkg/machinery/compatibility's KubernetesVersion.SupportedWith -- the
// "Kubernetes pairing" design section "Upgrades" names as a pre-check.
func KubernetesPairing(kubernetesVersion, talosVersion string) error {
	k8sVersion, err := compatibility.ParseKubernetesVersion(kubernetesVersion)
	if err != nil {
		return fmt.Errorf("parsing Kubernetes version %q: %w", kubernetesVersion, err)
	}

	talVersion, err := compatibility.ParseTalosVersion(&machine.VersionInfo{Tag: talosVersion})
	if err != nil {
		return fmt.Errorf("parsing Talos version %q: %w", talosVersion, err)
	}

	return k8sVersion.SupportedWith(talVersion)
}
