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

// Package flannel implements cni.Preset for TalosCluster.spec.cni.preset
// "flannel": Talos's own default CNI, which Ichor does nothing about
// (docs/design/architecture.md, "CNI"). Talos installs and manages it
// itself; there is nothing for Ichor to patch, render, hand to Argo CD or
// hand over, and it cannot run without kube-proxy.
package flannel

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// Preset implements cni.Preset for flannel. Every method is a no-op: the
// zero value is ready to use.
type Preset struct{}

var _ cni.Preset = Preset{}

// New returns a flannel Preset.
func New() Preset { return Preset{} }

// TalosPatches returns nil: flannel needs nothing from Talos beyond what
// Talos already does on its own. kubeProxy "replaced" is rejected, since
// flannel has no kube-proxy-free mode.
func (Preset) TalosPatches(kubeProxy cni.KubeProxyMode) ([]ichorv1alpha1.TalosConfigPatchDocument, error) {
	if kubeProxy == cni.KubeProxyReplaced {
		return nil, fmt.Errorf("flannel: kube-proxy replacement is not supported")
	}

	return nil, nil
}

// BootstrapManifest returns nil: Talos installs flannel itself, so Ichor
// renders nothing for the bootstrap.
func (Preset) BootstrapManifest(cni.KubeProxyMode) ([]byte, error) {
	return nil, nil
}

// ArgoCDApplication returns nil: flannel is never handed to Argo CD.
func (Preset) ArgoCDApplication(cni.KubeProxyMode) (*unstructured.Unstructured, error) {
	return nil, nil
}

// SupportsKubeProxyReplacement is always false: flannel has no
// kube-proxy-free mode.
func (Preset) SupportsKubeProxyReplacement() bool {
	return false
}

// CheckConsistency always succeeds: nothing is split across two owners, so
// there is nothing to compare.
func (Preset) CheckConsistency(context.Context, cni.LiveConfig, cni.KubeProxyMode) error {
	return nil
}

// HandoverWorkloads returns nil: there is no hand-over, since Ichor never
// bootstraps or manages flannel through Argo CD.
func (Preset) HandoverWorkloads() []cni.WorkloadRef {
	return nil
}
