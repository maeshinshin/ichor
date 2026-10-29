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

package cilium

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
)

func caps(names ...string) *corev1.SecurityContext {
	c := &corev1.Capabilities{}
	for _, n := range names {
		c.Add = append(c.Add, corev1.Capability(n))
	}

	return &corev1.SecurityContext{Capabilities: c}
}

// talosGuideCluster is a Cilium installed the way the Talos v1.14 guide's inline manifest does
// it (kube-proxy replaced): no Helm record, ipam kubernetes, no cgroup auto-mount, KubePrism.
func talosGuideCluster(image, ipam string) *fake.Clientset {
	ds := agentDaemonSet(image)
	spec := &ds.Spec.Template.Spec
	spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "KUBERNETES_SERVICE_HOST", Value: "localhost"},
		{Name: "KUBERNETES_SERVICE_PORT", Value: "7445"},
	}
	spec.Containers[0].SecurityContext = caps("CHOWN", "KILL", "NET_ADMIN", "NET_RAW", "IPC_LOCK",
		"SYS_ADMIN", "SYS_RESOURCE", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID")
	spec.InitContainers = []corev1.Container{
		{Name: "config"},
		{Name: "clean-cilium-state", SecurityContext: caps("NET_ADMIN", "SYS_ADMIN", "SYS_RESOURCE")},
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ReleaseNamespace, Name: "cilium-config"},
		Data: map[string]string{
			"ipam": ipam, "tunnel-protocol": "vxlan", "routing-mode": "tunnel",
			"kube-proxy-replacement": "true", "cgroup-root": "/sys/fs/cgroup",
		},
	}

	return fake.NewSimpleClientset(ds, cm)
}

func TestLiveValuesFromCluster_TalosGuideHasNoConflicts(t *testing.T) {
	kube := talosGuideCluster("quay.io/cilium/cilium:v"+ChartVersion, "kubernetes")

	live, err := LiveValuesFromCluster(t.Context(), kube, nil)
	if err != nil {
		t.Fatal(err)
	}

	cs, err := OwnedKeyConflicts(live, cni.KubeProxyReplaced, tunnelVxlan)
	if err != nil {
		t.Fatal(err)
	}

	if len(cs) != 0 {
		t.Errorf("conflicts = %v, want none for the Talos guide install", cs)
	}

	// The chart defaults alone would have claimed cluster-pool.
	assumed, _ := LiveValuesFromUser(nil)

	if cs, _ = OwnedKeyConflicts(assumed, cni.KubeProxyReplaced, tunnelVxlan); len(cs) == 0 {
		t.Error("control: chart defaults should conflict")
	}
}

func TestLiveValuesFromCluster_ClusterPoolIsStillRefused(t *testing.T) {
	kube := talosGuideCluster("quay.io/cilium/cilium:v"+ChartVersion, modeClusterPool)

	live, err := LiveValuesFromCluster(t.Context(), kube, nil)
	if err != nil {
		t.Fatal(err)
	}

	cs, _ := OwnedKeyConflicts(live, cni.KubeProxyReplaced, tunnelVxlan)

	if c := findConflict(cs, "ipam.mode"); c == nil || c.Level != Blocking {
		t.Fatalf("ipam.mode conflict = %+v, want blocking", c)
	}
}

func TestLiveValuesFromCluster_ReadsDefaultsOfAHelmStyleInstall(t *testing.T) {
	// Auto-mounted cgroup, no service host, default capabilities: every cgroup/service key differs.
	ds := agentDaemonSet("quay.io/cilium/cilium:v" + ChartVersion)
	ds.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "mount-cgroup"}}
	kube := fake.NewSimpleClientset(ds, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ReleaseNamespace, Name: "cilium-config"},
		Data:       map[string]string{"ipam": "kubernetes", "cgroup-root": "/run/cilium/cgroupv2", "kube-proxy-replacement": "false"},
	})

	live, err := LiveValuesFromCluster(t.Context(), kube, nil)
	if err != nil {
		t.Fatal(err)
	}

	cs, _ := OwnedKeyConflicts(live, cni.KubeProxyEnabled, tunnelVxlan)

	for _, key := range []string{"cgroup.autoMount.enabled", "cgroup.hostRoot", "securityContext.capabilities.ciliumAgent"} {
		if findConflict(cs, key) == nil {
			t.Errorf("no conflict for %s: %v", key, cs)
		}
	}

	if findConflict(cs, "ipam.mode") != nil {
		t.Errorf("ipam.mode conflicts: %v", cs)
	}
}

func TestConfigMapValuesAreHelmPaths(t *testing.T) {
	for key, path := range ConfigMapValues {
		if path == "" || key == "" {
			t.Errorf("%q -> %q", key, path)
		}
	}

	for path := range OwnedKeys {
		switch path {
		case "k8sServiceHost", "k8sServicePort", "cgroup.autoMount.enabled", "securityContext.capabilities.ciliumAgent",
			"securityContext.capabilities.cleanCiliumState":
			continue // read from the DaemonSet
		}

		found := false

		for _, p := range ConfigMapValues {
			found = found || p == path
		}

		if !found {
			t.Errorf("owned key %s is read from neither cilium-config nor the DaemonSet", path)
		}
	}
}

func TestPreflight_CaseCTalosGuideIsNotRefused(t *testing.T) {
	kube := talosGuideCluster("quay.io/cilium/cilium:v"+ChartVersion, "kubernetes")
	in := preflightInput(t, kube)
	in.KubeProxy = cni.KubeProxyReplaced

	var err error
	if in.Effective, err = EffectiveValues(nil, cni.KubeProxyReplaced, ""); err != nil {
		t.Fatal(err)
	}

	in.Get = newLiveCluster(t, in.Effective).get
	in.UserValues = nil

	rep, err := Preflight(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if len(rep.Conflicts) != 0 {
		t.Errorf("conflicts = %v", rep.Conflicts)
	}

	if err := rep.Err(false, false); err != nil {
		t.Errorf("Err = %v", err)
	}
}

func TestPreflight_CaseCClusterPoolRefused(t *testing.T) {
	kube := talosGuideCluster("quay.io/cilium/cilium:v"+ChartVersion, modeClusterPool)
	in := preflightInput(t, kube)
	in.KubeProxy = cni.KubeProxyReplaced
	in.UserValues = nil

	rep, err := Preflight(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}

	if err := rep.Err(true, true); err == nil {
		t.Error("a cluster-pool Cilium was accepted")
	}
}

func TestUserValues_PrunesEmptyMaps(t *testing.T) {
	in := map[string]any{
		"cgroup": map[string]any{"autoMount": map[string]any{"enabled": false}, "hostRoot": "/x"},
		"ipam":   map[string]any{"mode": "kubernetes"},
		"debug":  map[string]any{"enabled": true},
	}

	out := UserValues(in)

	if len(out) != 1 || out["debug"] == nil {
		t.Errorf("out = %v, want only debug", out)
	}
}

func TestVersionCheckString(t *testing.T) {
	got := VersionCheck{Live: "1.18.0", Target: "1.18.0"}.String()
	if got != "live version 1.18.0, chart 1.18.0: the same minor" {
		t.Errorf("got %q", got)
	}

	if got := (VersionCheck{Live: "1.17.5", Target: "1.18.0", Upgrade: true}).String(); got !=
		"live version 1.17.5, chart 1.18.0: one minor below (adoption is an upgrade)" {
		t.Errorf("got %q", got)
	}
}
