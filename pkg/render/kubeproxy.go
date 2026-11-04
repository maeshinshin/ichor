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

package render

import (
	"reflect"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mv1alpha1 "github.com/siderolabs/talos/pkg/machinery/config/types/v1alpha1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// KubeProxyMode is the mode the Talos side of the kube-proxy split renders (ADR 0066):
// status.cni.kubeProxy while a switch has started, otherwise spec.cni.kubeProxy. The second
// return is true only when status.cni.kubeProxy is what decided it.
func KubeProxyMode(cluster *ichorv1alpha1.TalosCluster) (mode cni.KubeProxyMode, fromStatus bool) {
	if s := cluster.Status.CNI; s != nil && s.KubeProxy != "" {
		return cni.KubeProxyMode(s.KubeProxy), true
	}

	return cni.KubeProxyMode(cluster.Spec.CNI.KubeProxy), false
}

// kubeProxyOwned reports whether Ichor, not the patches, decides cluster.proxy.disabled: from the
// moment the cluster replaces kube-proxy or a switch has set the status. A cluster that is enabled
// and never switched renders exactly as before, whatever its patches say.
func kubeProxyOwned(cluster *ichorv1alpha1.TalosCluster) bool {
	mode, fromStatus := KubeProxyMode(cluster)

	return fromStatus || mode == cni.KubeProxyReplaced
}

// dropProxyDisabled returns ordered with cluster.proxy.disabled cleared from every Config
// document, deep-copying only the patches that set it. The legacy block cannot coexist with the KubeProxyConfig
// document a v1.14 contract renders, and the value is owned by injectKubeProxy now.
func dropProxyDisabled(ordered []*ichorv1alpha1.TalosConfigPatch) []*ichorv1alpha1.TalosConfigPatch {
	return rewriteConfigs(ordered,
		func(c *typedconfig.Config) bool {
			return c.ClusterConfig != nil && c.ClusterConfig.ProxyConfig != nil && c.ClusterConfig.ProxyConfig.Disabled != nil
		},
		func(c *typedconfig.Config) {
			cc := c.ClusterConfig
			cc.ProxyConfig.Disabled = nil

			if reflect.DeepEqual(*cc.ProxyConfig, typedconfig.ProxyConfig{}) {
				cc.ProxyConfig = nil
			}
		})
}

// injectKubeProxy renders the Talos side of the kube-proxy split. "replaced" always renders
// disabled; "enabled" renders an explicit enabled only once a switch has set the status (a
// cluster that never switched keeps Talos's default, so its configuration hash does not move).
// It runs after the patches so a stale patch cannot win.
func injectKubeProxy(
	cfg coreconfig.Provider, contract *coreconfig.VersionContract, cluster *ichorv1alpha1.TalosCluster,
) (coreconfig.Provider, error) {
	mode, fromStatus := KubeProxyMode(cluster)

	if mode != cni.KubeProxyReplaced && (!fromStatus || mode != cni.KubeProxyEnabled) {
		return cfg, nil
	}

	// Talos validates KubeProxyConfig as control-plane-only (the bootstrap manifests that carry
	// kube-proxy are rendered by the control planes), so a worker gets no document at v1.14.
	if contract.MultidocKubernetesConfigSupported() && cfg.Machine().Type() == machine.TypeWorker {
		return cfg, nil
	}

	enabled := mode == cni.KubeProxyEnabled
	disabled := !enabled

	var doc config.Document

	if contract.MultidocKubernetesConfigSupported() {
		d := k8s.NewKubeProxyConfigV1Alpha1()
		d.ProxyEnabled = &enabled
		doc = d
	} else {
		//nolint:staticcheck // the legacy block is the only form a pre-1.14 contract accepts
		doc = &mv1alpha1.Config{
			ConfigVersion: mv1alpha1.Version,
			ClusterConfig: &mv1alpha1.ClusterConfig{ProxyConfig: &mv1alpha1.ProxyConfig{Disabled: &disabled}},
		}
	}

	return mergeDocument(cfg, "kube-proxy mode", doc)
}
