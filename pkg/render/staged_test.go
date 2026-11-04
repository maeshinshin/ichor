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
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestStagedKubernetesPinsKubeletAndProxyBelowTheSpec checks: during a staged Kubernetes
// upgrade the control-plane components follow spec.kubernetesVersion while the kubelet (and
// kube-proxy) can be pinned to the version the node still runs, and with nothing pinned the
// render is byte for byte what it was.
func TestStagedKubernetesPinsKubeletAndProxyBelowTheSpec(t *testing.T) {
	const old, target = "v1.35.4", "v1.36.0"

	bundle := testBundle(t)
	cluster := testCluster("true")
	cluster.Spec.KubernetesVersion = target

	cp := testMachine("node-a", roleControlPlane, nil)
	worker := testMachine("node-w", roleWorker, nil)

	plain := mustRender(t, cluster, cp, bundle, nil)

	render := func(t *testing.T, m *ichorv1alpha1.TalosMachine, staged *StagedKubernetes) string {
		t.Helper()

		return mustRenderSys(t, cluster, m, bundle, nil, &SystemInputs{Staged: staged})
	}

	t.Run("an empty pin changes nothing", func(t *testing.T) {
		if got := render(t, cp, &StagedKubernetes{}); got != plain {
			t.Fatalf("an empty StagedKubernetes must render the plain configuration")
		}
	})

	t.Run("a control plane keeps its components at the target and its kubelet and proxy at the pin", func(t *testing.T) {
		got := render(t, cp, &StagedKubernetes{KubeletVersion: old, KubeProxyVersion: old})

		requireAll(t, got, "kube-apiserver:"+target, "kube-controller-manager:"+target, "kube-scheduler:"+target,
			"kubelet:"+old, "kube-proxy:"+old)
		requireNone(t, got, "kubelet:"+target, "kube-proxy:"+target, "kube-apiserver:"+old)
	})

	t.Run("pinning only the kubelet leaves kube-proxy on the target", func(t *testing.T) {
		requireAll(t, render(t, cp, &StagedKubernetes{KubeletVersion: old}), "kubelet:"+old, "kube-proxy:"+target)
	})

	t.Run("controller-manager and scheduler can be pinned below the apiserver", func(t *testing.T) {
		got := render(t, cp, &StagedKubernetes{ControllerManagerVersion: old, SchedulerVersion: old})

		requireAll(t, got,
			"kube-apiserver:"+target, "kube-controller-manager:"+old, "kube-scheduler:"+old, "kubelet:"+target)
	})

	t.Run("a worker pins its kubelet and gets no kube-proxy document", func(t *testing.T) {
		got := render(t, worker, &StagedKubernetes{KubeletVersion: old, KubeProxyVersion: old})

		requireAll(t, got, "kubelet:"+old)
		requireNone(t, got, "kube-proxy")
	})

	t.Run("the pin is the version without a leading v too", func(t *testing.T) {
		requireAll(t, render(t, cp, &StagedKubernetes{KubeletVersion: strings.TrimPrefix(old, "v")}), "kubelet:"+old)
	})
}
