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

// Package cilium implements cni.Preset for TalosCluster.spec.cni.preset
// "cilium": Helm, with Talos's recommended values built in
// (docs/design/architecture.md, "CNI"). The bootstrap manifest is the pinned
// chart, embedded in the binary, expanded with those values by Helm's Go SDK
// (render.go); the Argo CD hand-over uses the same chart coordinates and
// values (values.go).
package cilium

import (
	"context"
	"fmt"

	"k8s.io/utils/ptr"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/gitops"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// argoCDApplicationNamespace and argoCDDestinationServer are where the
// cilium Application resource itself lives, and the in-cluster API server
// address it targets -- the same two literals the Application's own
// metadata.namespace and spec.destination.server carried before this was
// a typed gitops.Application.
const (
	argoCDApplicationNamespace = "argocd"
	argoCDDestinationServer    = "https://kubernetes.default.svc"
)

// namespace is where Cilium's chart installs, and where the live
// ConfigMap CheckConsistency's caller is expected to read from lives.
const namespace = "kube-system"

// Preset implements cni.Preset for Cilium. The zero value is ready to use.
type Preset struct {
	// tunnel is the tunnel protocol ("" keeps Cilium's default, vxlan).
	tunnel string
	// user is spec.cni.cilium.values (WithValues).
	user map[string]any
}

var _ cni.Preset = Preset{}

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

// TalosPatches returns the cluster.proxy.disabled document when kubeProxy
// is "replaced" -- the Talos-side half of the two-owner split
// (docs/design/architecture.md, "CNI") -- and nothing when it is
// "enabled", since that is already Talos's default.
func (Preset) TalosPatches(kubeProxy cni.KubeProxyMode) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if err := kubeProxy.Validate(); err != nil {
		return nil, err
	}

	if kubeProxy != cni.KubeProxyReplaced {
		return nil, nil
	}

	return []ichorv1alpha1.TalosConfigPatchDocument{
		{
			Kind: "Config",
			Config: &typedconfig.Config{
				ClusterConfig: &typedconfig.ClusterConfig{
					ProxyConfig: &typedconfig.ProxyConfig{Disabled: ptr.To(true)},
				},
			},
		},
	}, nil
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

// ArgoCDApplication renders the Argo CD Application that takes over from
// the bootstrap manifest: the same chart coordinates and values, plus the
// known pitfalls for a CRD-heavy Helm chart -- server-side apply, so the
// CRDs don't hit the annotation size limit a plain apply would
// (argo-cd.readthedocs.io, "Sync Options", retrieved 2026-10-25) -- and the
// Hubble CronJob setting already in Values (values.go), so Argo CD never
// sees a diff Helm itself introduced.
func (p Preset) ArgoCDApplication(kubeProxy cni.KubeProxyMode) (*gitops.Application, error) {
	if err := kubeProxy.Validate(); err != nil {
		return nil, err
	}

	valuesYAML, err := sigsyaml.Marshal(p.valuesFor(kubeProxy))
	if err != nil {
		return nil, fmt.Errorf("cilium: rendering Helm values: %w", err)
	}

	app, err := gitops.New(gitops.Options{
		Name:      ChartName,
		Namespace: argoCDApplicationNamespace,
		Source: &gitops.ApplicationSource{
			RepoURL:        ChartRepoURL,
			Chart:          ChartName,
			TargetRevision: ChartVersion,
			Helm:           &gitops.ApplicationSourceHelm{Values: string(valuesYAML)},
		},
		Destination:     gitops.ApplicationDestination{Server: argoCDDestinationServer, Namespace: namespace},
		ServerSideApply: true,
		IgnoreDifferences: []gitops.ResourceIgnoreDifference{
			{Group: "cilium.io", Kind: "CiliumIdentity"},
		},
		// Automated sync, or Argo CD never syncs this Application and the
		// hand-over never completes (ADR 0055 addendum). PruneLast deletes
		// anything the chart dropped only after the rest is healthy.
		Automated:        true,
		Prune:            true,
		SelfHeal:         true,
		ExtraSyncOptions: []string{"CreateNamespace=true", "PruneLast=true"},
		SyncWave:         gitops.SyncWaveCNI,
	})
	if err != nil {
		return nil, fmt.Errorf("cilium: building Argo CD Application: %w", err)
	}

	return app, nil
}

// SupportsKubeProxyReplacement is always true: Cilium can run without
// kube-proxy.
func (Preset) SupportsKubeProxyReplacement() bool {
	return true
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

// HandoverWorkloads returns the cilium DaemonSet and cilium-operator
// Deployment: Cilium's own agent and operator, whose readiness is the
// standard signal the CNI is up (docs.cilium.io / Cilium's own install
// verification, retrieved 2026-10-25).
func (Preset) HandoverWorkloads() []cni.WorkloadRef {
	return []cni.WorkloadRef{
		{Namespace: namespace, Kind: "DaemonSet", Name: "cilium"},
		{Namespace: namespace, Kind: "Deployment", Name: "cilium-operator"},
	}
}
