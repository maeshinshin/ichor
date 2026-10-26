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

// TestStagedKubernetesPinsKubeletAndProxyBelowTheSpec checks ADR 0087: during a staged Kubernetes
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

		out, _, err := Render(cluster, m, bundle, nil, &SystemInputs{Staged: staged})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		return string(out)
	}

	t.Run("an empty pin changes nothing", func(t *testing.T) {
		if got := render(t, cp, &StagedKubernetes{}); got != plain {
			t.Fatalf("an empty StagedKubernetes must render the plain configuration")
		}
	})

	t.Run("a control plane keeps its components at the target and its kubelet and proxy at the pin", func(t *testing.T) {
		got := render(t, cp, &StagedKubernetes{KubeletVersion: old, KubeProxyVersion: old})

		for _, want := range []string{
			"kube-apiserver:" + target, "kube-controller-manager:" + target, "kube-scheduler:" + target,
			"kubelet:" + old, "kube-proxy:" + old,
		} {
			if !strings.Contains(got, want) {
				t.Errorf("want %q in the staged render", want)
			}
		}

		for _, bad := range []string{"kubelet:" + target, "kube-proxy:" + target, "kube-apiserver:" + old} {
			if strings.Contains(got, bad) {
				t.Errorf("did not want %q in the staged render", bad)
			}
		}
	})

	t.Run("pinning only the kubelet leaves kube-proxy on the target", func(t *testing.T) {
		got := render(t, cp, &StagedKubernetes{KubeletVersion: old})
		if !strings.Contains(got, "kubelet:"+old) || !strings.Contains(got, "kube-proxy:"+target) {
			t.Fatalf("want kubelet at %s and kube-proxy at %s", old, target)
		}
	})

	t.Run("a worker pins its kubelet and gets no kube-proxy document", func(t *testing.T) {
		got := render(t, worker, &StagedKubernetes{KubeletVersion: old, KubeProxyVersion: old})
		if !strings.Contains(got, "kubelet:"+old) || strings.Contains(got, "kube-proxy") {
			t.Fatalf("want kubelet at %s and no kube-proxy, got a different render", old)
		}
	})

	t.Run("the pin is the version without a leading v too", func(t *testing.T) {
		got := render(t, cp, &StagedKubernetes{KubeletVersion: strings.TrimPrefix(old, "v")})
		if !strings.Contains(got, "kubelet:"+old) {
			t.Fatalf("want kubelet at %s", old)
		}
	})
}
