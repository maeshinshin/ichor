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

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/pkg/cni"
)

func testDS(name, image string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		Namespace: k8sFactsSystemNamespace, Name: name,
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}},
		}},
	}
}

func helmRecord(ns string) *corev1.Secret {
	return &corev1.Secret{
		Namespace: ns, Name: "sh.helm.release.v1.cilium.v3",
		Labels: map[string]string{"owner": "helm", helmNameLabel: "cilium"},
		Type:   "helm.sh/release.v1",
	}
}

func ciliumCM(kpr string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		Namespace: k8sFactsSystemNamespace, Name: "cilium-config",
		Data: map[string]string{"kube-proxy-replacement": kpr},
	}
}

func factsFor(t *testing.T, objs ...client.Object) *k8sFacts {
	t.Helper()

	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()

	f, err := gatherK8sFacts(context.Background(), c)
	if err != nil {
		t.Fatalf("gatherK8sFacts: %v", err)
	}

	return f
}

const (
	tCustom      = "custom"
	ciliumImage  = "quay.io/cilium/cilium:v1.18.2@sha256:abc"
	flannelImage = "ghcr.io/flannel-io/flannel:v0.26.0"
	calicoImage  = "docker.io/calico/node:v3.29.0"
)

func TestDetectCNI_Table(t *testing.T) {
	crd := &unstructured.Unstructured{}
	crd.SetGroupVersionKind(crdGVK)
	crd.SetName("ciliumnodes.cilium.io")

	tests := []struct {
		name       string
		objs       []client.Object
		hint       talosCNIHint
		wantKind   cniKind
		wantProv   cniProvenance
		wantMixed  bool
		wantNodeCR bool
	}{
		{"cilium ichor", []client.Object{testDS("cilium", ciliumImage), helmRecord("ichor-system")}, talosCNIHint{Name: string(detectedNone)}, detectedCilium, provenanceIchor, false, false},
		{"cilium helm", []client.Object{testDS("cilium", ciliumImage), helmRecord("kube-system")}, talosCNIHint{}, detectedCilium, provenanceHelm, false, false},
		{"cilium manifest", []client.Object{testDS("cilium", ciliumImage), crd}, talosCNIHint{Name: tCustom}, detectedCilium, provenanceManifest, false, true},
		{"flannel running", []client.Object{testDS("kube-flannel", flannelImage)}, talosCNIHint{}, detectedFlannel, "", false, false},
		{"flannel not yet running", nil, talosCNIHint{}, detectedFlannel, "", false, false},
		{"calico", []client.Object{testDS("calico-node", calicoImage)}, talosCNIHint{Name: tCustom}, detectedCalico, "", false, false},
		{"none", nil, talosCNIHint{Name: string(detectedNone)}, detectedNone, "", false, false},
		{"mixed", []client.Object{testDS("cilium", ciliumImage), testDS("calico-node", calicoImage)}, talosCNIHint{}, detectedCalico, "", true, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := factsFor(t, tc.objs...)
			rep := detectCNI(f, tc.hint)

			if rep.Kind != tc.wantKind || rep.Provenance != tc.wantProv || rep.Mixed() != tc.wantMixed {
				t.Fatalf("got kind=%s prov=%s mixed=%v, want kind=%s prov=%s mixed=%v", rep.Kind, rep.Provenance, rep.Mixed(), tc.wantKind, tc.wantProv, tc.wantMixed)
			}

			if f.CiliumNodeCRD != tc.wantNodeCR {
				t.Errorf("CiliumNodeCRD = %v, want %v", f.CiliumNodeCRD, tc.wantNodeCR)
			}
		})
	}
}

func TestDetectCNI_VersionAndCustomURLs(t *testing.T) {
	rep := detectCNI(factsFor(t, testDS("cilium", ciliumImage)), talosCNIHint{})
	if rep.Version != "v1.18.2" {
		t.Errorf("Version = %q, want v1.18.2", rep.Version)
	}

	rep = detectCNI(factsFor(t), talosCNIHint{Name: "custom", URLs: []string{"https://x/y.yaml"}})
	if !rep.CustomURLs {
		t.Error("custom with URLs not flagged")
	}
}

func TestCheckCNIAdoption(t *testing.T) {
	flannel := detectCNI(factsFor(t, testDS("kube-flannel", flannelImage)), talosCNIHint{})
	cilium := detectCNI(factsFor(t, testDS("cilium", ciliumImage)), talosCNIHint{})
	mixed := detectCNI(factsFor(t, testDS("cilium", ciliumImage), testDS("calico-node", calicoImage)), talosCNIHint{})

	if err := checkCNIAdoption(flannel, false); err == nil || !strings.Contains(err.Error(), "--cni-unmanaged") || !strings.Contains(err.Error(), "flannel") {
		t.Errorf("flannel without flag: err = %v, want refusal naming flannel and --cni-unmanaged", err)
	}

	if err := checkCNIAdoption(flannel, true); err != nil {
		t.Errorf("flannel with flag: %v", err)
	}

	if err := checkCNIAdoption(cilium, false); err != nil {
		t.Errorf("cilium managed: %v", err)
	}

	if err := checkCNIAdoption(cilium, true); err != nil {
		t.Errorf("cilium unmanaged: %v", err)
	}

	for _, unmanaged := range []bool{false, true} {
		err := checkCNIAdoption(mixed, unmanaged)
		if err == nil || !strings.Contains(err.Error(), "kube-system/cilium") || !strings.Contains(err.Error(), "kube-system/calico-node") {
			t.Errorf("mixed (unmanaged=%v): err = %v, want refusal naming both", unmanaged, err)
		}
	}
}

func TestRunAdopt_RefusesNonCiliumWithNothingWritten(t *testing.T) {
	target := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(testDS("calico-node", calicoImage)).Build()

	var errOut bytes.Buffer

	err := runAdopt(context.Background(), "c", "default", adoptDeps{Target: target, Stderr: &errOut})
	if err == nil || !strings.Contains(err.Error(), "--cni-unmanaged") {
		t.Fatalf("err = %v, want the unmanaged refusal", err)
	}
}

func TestDetectKubeProxy(t *testing.T) {
	tests := []struct {
		name         string
		kpr          string
		talosEnabled bool
		unmanaged    bool
		want         string
		wantErr      bool
	}{
		{"enabled, replacement off", "false", true, false, string(cni.KubeProxyEnabled), false},
		{"disabled, replacement on", kprTrue, false, false, string(cni.KubeProxyReplaced), false},
		{"legacy strict", "strict", false, false, string(cni.KubeProxyReplaced), false},
		{"disabled, replacement off", "false", false, false, "", true},
		{"enabled, replacement on", kprTrue, true, false, "", true},
		{"partial", "partial", true, false, "", true},
		{"unmanaged enabled", "", true, true, string(cni.KubeProxyEnabled), false},
		{"unmanaged replaced", "", false, true, string(cni.KubeProxyReplaced), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f *k8sFacts
			if tc.kpr == "" {
				f = factsFor(t)
			} else {
				f = factsFor(t, ciliumCM(tc.kpr))
			}

			got, err := detectKubeProxy(f, tc.talosEnabled, tc.unmanaged)
			if (err != nil) != tc.wantErr || got.Mode != tc.want {
				t.Fatalf("got %q, %v; want %q, err=%v", got.Mode, err, tc.want, tc.wantErr)
			}
		})
	}

	if _, err := detectKubeProxy(factsFor(t), true, false); err == nil {
		t.Error("managed with no cilium-config: want an error")
	}
}

func TestGatherK8sFacts_KubeProxyDaemonSet(t *testing.T) {
	if !factsFor(t, testDS("kube-proxy", "registry.k8s.io/kube-proxy:v1.34.0")).KubeProxyDaemonSet {
		t.Error("kube-proxy DaemonSet not seen")
	}

	if factsFor(t).KubeProxyDaemonSet {
		t.Error("kube-proxy DaemonSet seen on an empty cluster")
	}
}
