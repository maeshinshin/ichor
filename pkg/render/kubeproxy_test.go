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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// proxyDisabledPatch is what `ichor init` writes for a cluster created with kubeProxy replaced:
// a cluster-layer patch with cluster.proxy.disabled true.
func proxyDisabledPatch() ichorv1alpha1.TalosConfigPatch {
	disabled := true

	return ichorv1alpha1.TalosConfigPatch{
		Namespace: testNamespace, Name: "cni-proxy",
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName},
			Layer:      layerCluster,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind: kindConfig,
				Config: &typedconfig.Config{
					ConfigVersion: configVersionV1Alpha1,
					ClusterConfig: &typedconfig.ClusterConfig{ProxyConfig: &typedconfig.ProxyConfig{Disabled: &disabled}},
				},
			}},
		},
	}
}

// TestKubeProxyModeIsRenderedByIchor checks ADR 0066: the Talos side follows status.cni.kubeProxy
// once a switch set it, spec.cni.kubeProxy before that, and never a patch; a cluster that is
// enabled and never switched keeps Talos's default (nothing rendered).
func TestKubeProxyModeIsRenderedByIchor(t *testing.T) {
	bundle := testBundle(t)
	m := testMachine("node-a", roleControlPlane, nil)
	patches := []ichorv1alpha1.TalosConfigPatch{proxyDisabledPatch()}

	// The v1.14 contract renders the KubeProxyConfig document's enabled flag.
	const proxyOff, proxyOn = "enabled: false", "enabled: true"

	proxyDoc := func(out string) string {
		_, after, _ := strings.Cut(out, "kind: KubeProxyConfig")
		doc, _, _ := strings.Cut(after, "---")

		return doc
	}

	clusterFor := func(spec, status cni.KubeProxyMode) *ichorv1alpha1.TalosCluster {
		c := testCluster("true")
		c.Spec.CNI.KubeProxy = string(spec)

		if status != "" {
			c.Status.CNI = &ichorv1alpha1.TalosClusterCNIStatus{KubeProxy: string(status)}
		}

		return c
	}

	t.Run("spec replaced renders disabled with no patch", func(t *testing.T) {
		requireAll(t, mustRender(t, clusterFor(cni.KubeProxyReplaced, ""), m, bundle, nil), proxyOff)
	})

	t.Run("a legacy patch that disables it no longer breaks the v1.14 render", func(t *testing.T) {
		requireAll(t, mustRender(t, clusterFor(cni.KubeProxyReplaced, ""), m, bundle, patches), proxyOff)
	})

	t.Run("spec enabled and never switched renders Talos's default", func(t *testing.T) {
		requireNone(t, proxyDoc(mustRender(t, clusterFor(cni.KubeProxyEnabled, ""), m, bundle, nil)), proxyOff, proxyOn)
	})

	t.Run("spec enabled and never switched still honours a patch", func(t *testing.T) {
		worker := testMachine("node-w", roleWorker, nil)

		requireAll(t, mustRender(t, clusterFor(cni.KubeProxyEnabled, ""), worker, bundle, patches), "disabled: true")
	})

	t.Run("a worker renders no KubeProxyConfig: Talos allows the document on control planes only", func(t *testing.T) {
		worker := testMachine("node-w", roleWorker, nil)

		requireNone(t, mustRender(t, clusterFor(cni.KubeProxyReplaced, cni.KubeProxyReplaced), worker, bundle, nil),
			"kind: KubeProxyConfig")
	})

	t.Run("status wins over spec in both directions, and over a patch", func(t *testing.T) {
		out := mustRender(t, clusterFor(cni.KubeProxyReplaced, cni.KubeProxyEnabled), m, bundle, patches)
		requireAll(t, proxyDoc(out), proxyOn)

		requireAll(t, mustRender(t, clusterFor(cni.KubeProxyEnabled, cni.KubeProxyReplaced), m, bundle, nil), proxyOff)
	})

	t.Run("does not mutate the caller's patch", func(t *testing.T) {
		before := patches[0].DeepCopy()
		_ = mustRender(t, clusterFor(cni.KubeProxyReplaced, ""), m, bundle, patches)

		if !reflect.DeepEqual(before.Spec, patches[0].Spec) {
			t.Fatal("Render mutated the caller's TalosConfigPatch")
		}
	})
}
