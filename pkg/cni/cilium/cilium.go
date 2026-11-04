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

// Package cilium is the CNI for TalosCluster.spec.cni.preset
// "cilium": Helm, with Talos's recommended values built in
// (docs/design/architecture.md, "CNI"). The bootstrap manifest is the pinned
// chart, embedded in the binary, expanded with those values by Helm's Go SDK
// (render.go).
package cilium

import (
	"context"
	"fmt"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// namespace is where Cilium's chart installs, and where the live
// ConfigMap CheckConsistency's caller is expected to read from lives.
const namespace = "kube-system"

// Preset is the Cilium CNI. The zero value is ready to use.
type Preset struct {
	// tunnel is the tunnel protocol ("" keeps Cilium's default, vxlan).
	tunnel string
	// user is spec.cni.cilium.values (WithValues).
	user map[string]any
	// controlPlanes is the cluster's control-plane count; 0 means unknown (the chart's defaults).
	controlPlanes int
	// nodes is how many Nodes the cluster has right now; 0 means unknown (WithNodes).
	nodes int
}

// WithNodes returns a copy of p for a cluster that has n Nodes now. A cluster with one Node runs one
// operator replica whatever its desired control-plane count: it is bootstrapped with one Node and the
// others join later, and a second replica without a Node blocks every rollout of the operator (values.go).
func (p Preset) WithNodes(n int) Preset {
	p.nodes = n

	return p
}

// WithControlPlanes returns a copy of p for a cluster with n control planes. One control plane pins the
// operator to one replica (values.go, operator); any other count, or 0, keeps the chart's default.
func (p Preset) WithControlPlanes(n int) Preset {
	p.controlPlanes = n

	return p
}

// New returns a Cilium Preset.
func New() Preset { return Preset{} }

// WithTunnel returns a copy of p that sets Cilium's tunnelProtocol ("vxlan"
// or "geneve"; "" keeps the chart default, vxlan). Some virtualised networks
// pass geneve but not vxlan between nodes.
func (p Preset) WithTunnel(protocol string) (Preset, error) {
	switch protocol {
	case "", "vxlan", "geneve":
		p.tunnel = protocol
		return p, nil
	default:
		return p, fmt.Errorf("cilium: unknown tunnel protocol %q, want vxlan or geneve", protocol)
	}
}

// TalosPatches returns nothing for either mode: the Talos side of the kube-proxy split
// (KubeProxyConfig enabled on v1.14, cluster.proxy.disabled before) is rendered by
// pkg/render's injectKubeProxy from spec.cni.kubeProxy, its single owner (ADR 0099).
func (Preset) TalosPatches(kubeProxy cni.KubeProxyMode) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if err := kubeProxy.Validate(); err != nil {
		return nil, err
	}

	return nil, nil
}

// BootstrapManifest renders the pinned Cilium chart (ChartVersion, embedded in
// the binary) with the preset's values for kubeProxy into plain manifests
// that Talos can apply from cluster.inlineManifests (ADR 0098).
func (p Preset) BootstrapManifest(kubeProxy cni.KubeProxyMode) ([]byte, error) {
	if err := kubeProxy.Validate(); err != nil {
		return nil, err
	}

	effective, err := p.effective(kubeProxy)
	if err != nil {
		return nil, err
	}

	return renderChart(effective)
}

// CheckConsistency compares live's kube-proxy-replacement state against
// what kubeProxy declares. A mismatch is the two-owner split
// (docs/design/architecture.md, "CNI") disagreeing with itself: Talos's
// side (cluster.proxy.disabled) says one thing and Cilium's live
// configuration says another.
func (Preset) CheckConsistency(_ context.Context, live cni.LiveConfig, kubeProxy cni.KubeProxyMode) error {
	if err := kubeProxy.Validate(); err != nil {
		return err
	}

	want := kubeProxy == cni.KubeProxyReplaced
	if live.KubeProxyReplacement != want {
		return fmt.Errorf(
			"cilium: live kube-proxy-replacement is %t, but spec.cni.kubeProxy is %q",
			live.KubeProxyReplacement, kubeProxy,
		)
	}

	return nil
}
