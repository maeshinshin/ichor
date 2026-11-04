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
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	serverTLSBootstrap = "serverTLSBootstrap: true"
	kubeletCAPath      = "/system/secrets/kubernetes/kube-apiserver/ca.crt"
)

func kubeletServingSecurity(k *ichorv1alpha1.TalosClusterKubeletServing) *ichorv1alpha1.TalosClusterSecurity {
	return &ichorv1alpha1.TalosClusterSecurity{KubeletServingCertificates: k}
}

func kubeletServingCluster(contract string, verify, latched bool) *ichorv1alpha1.TalosCluster {
	c := secCluster(kubeletServingSecurity(&ichorv1alpha1.TalosClusterKubeletServing{
		Enabled: true, VerifyFromAPIServer: verify,
	}))
	if contract != "" {
		c.Spec.VersionContract = contract
	}

	if latched {
		apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type: ichorv1alpha1.ConditionKubeletServingVerified, Status: metav1.ConditionTrue, Reason: "AllNodesIssued",
		})
	}

	return c
}

// TestKubeletServingRendering: ADR 0125. serverTLSBootstrap is rendered on every node in the
// spelling the contract takes, and --kubelet-certificate-authority only on control planes and only
// once the KubeletServingVerified latch is set.
func TestKubeletServingRendering(t *testing.T) {
	bundle := testBundle(t)
	cp := testMachine("cp", roleControlPlane, nil)
	worker := testMachine("w", roleWorker, nil)

	t.Run("off renders the same bytes", func(t *testing.T) {
		for _, m := range []*ichorv1alpha1.TalosMachine{cp, worker} {
			base := mustRender(t, testCluster("true"), m, bundle, nil)
			off := secCluster(kubeletServingSecurity(&ichorv1alpha1.TalosClusterKubeletServing{}))

			if got := mustRender(t, off, m, bundle, nil); got != base {
				t.Errorf("%s: a disabled option changed the render", m.Name)
			}

			requireNone(t, base, serverTLSBootstrap, kubeletCAFlag)
		}
	})

	t.Run("v1.14 renders the KubeletConfig document on every node", func(t *testing.T) {
		for _, m := range []*ichorv1alpha1.TalosMachine{cp, worker} {
			doc := section(mustRender(t, kubeletServingCluster("", false, false), m, bundle, nil), "kind: KubeletConfig")
			requireAll(t, doc, serverTLSBootstrap, "image: ghcr.io/siderolabs/kubelet:")
			requireNone(t, doc, kubeletCAFlag)
		}
	})

	t.Run("a pre-1.14 contract renders machine.kubelet.extraConfig", func(t *testing.T) {
		for _, m := range []*ichorv1alpha1.TalosMachine{cp, worker} {
			out := mustRender(t, kubeletServingCluster(contractV113, false, false), m, bundle, nil)
			requireAll(t, out, "extraConfig:", serverTLSBootstrap)
			requireNone(t, out, "kind: KubeletConfig")
		}
	})

	t.Run("the apiserver flag waits for the latch and is for control planes only", func(t *testing.T) {
		requireNone(t, mustRender(t, kubeletServingCluster("", true, false), cp, bundle, nil), kubeletCAFlag)

		doc := section(mustRender(t, kubeletServingCluster("", true, true), cp, bundle, nil), "kind: KubeAPIServerConfig")
		requireAll(t, doc, kubeletCAFlag, kubeletCAPath)

		requireNone(t, mustRender(t, kubeletServingCluster("", true, true), worker, bundle, nil), kubeletCAFlag)
	})

	t.Run("the apiserver flag on a pre-1.14 contract", func(t *testing.T) {
		out := mustRender(t, kubeletServingCluster(contractV113, true, true), cp, bundle, nil)
		requireAll(t, out, "apiServer:", kubeletCAFlag, kubeletCAPath)
	})

	t.Run("the latch alone does nothing when verification is not asked for", func(t *testing.T) {
		requireNone(t, mustRender(t, kubeletServingCluster("", false, true), cp, bundle, nil), kubeletCAFlag)
	})

	t.Run("verifyFromAPIServer without enabled is refused", func(t *testing.T) {
		c := kubeletServingCluster("", true, false)
		c.Spec.Security.KubeletServingCertificates.Enabled = false

		if err := ValidateSecurity(c); err == nil {
			t.Fatal("want an error")
		}
	})
}
