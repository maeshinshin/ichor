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

// Package cni holds the types shared by the CNI code (docs/design/architecture.md,
// "CNI"); Cilium is the only CNI (ADR 0099). Ichor bootstraps it from an inline manifest and then owns it as a
// Helm release run by the ichor-cni Job (ADR 0101); nothing here reaches into a running cluster on its own.
package cni

import (
	"fmt"
)

// InlineManifestName is the name `ichor init` writes the CNI's bootstrap
// manifest under in cluster.inlineManifests (internal/cli's own cniKey,
// unexported there and duplicated here because that package and this
// one's callers -- pkg/render, internal/controller -- have no shared home
// below internal/cli that isn't a layering violation). The hand-over
// reconciler (ADR 0024) matches on this exact name to know which
// inlineManifests entry to stop rendering once the Helm release owns it.
const InlineManifestName = "cni"

// KubeProxyMode is TalosCluster.spec.cni.kubeProxy: kube-proxy stays Talos's
// own, or the CNI replaces it.
type KubeProxyMode string

const (
	// KubeProxyEnabled is Talos's own default: kube-proxy runs.
	KubeProxyEnabled KubeProxyMode = "enabled"

	// KubeProxyReplaced means the CNI takes over kube-proxy's job. The
	// setting then has two owners -- Talos (cluster.proxy.disabled, written
	// by Ichor) and the CNI (written by its Helm release) -- which is exactly what
	// cilium.Preset.CheckConsistency exists to keep honest.
	KubeProxyReplaced KubeProxyMode = "replaced"
)

// Validate reports whether m is one of the two values the CRD's enum
// allows. Callers that only ever see values the API server already
// validated don't need this; it exists for callers outside the API server, e.g. from a unit test.
func (m KubeProxyMode) Validate() error {
	switch m {
	case KubeProxyEnabled, KubeProxyReplaced:
		return nil
	default:
		return fmt.Errorf("cni: kubeProxy %q is invalid: must be %q or %q", m, KubeProxyEnabled, KubeProxyReplaced)
	}
}

// LiveConfig is what cilium.Preset.CheckConsistency compares against: the CNI's
// own running configuration, as read by the caller (the future reconciler;
// reading it is not this package's job -- see CheckConsistency).
type LiveConfig struct {
	// KubeProxyReplacement is the CNI's own live notion of whether it has
	// taken over kube-proxy's job, e.g. read from Cilium's cilium-config
	// ConfigMap.
	KubeProxyReplacement bool
}
