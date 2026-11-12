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

package typedconfig_test

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/types/k8s"
	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestSubsetRefusesWhatItOmits reads Talos documents that set something the typed form does not carry, a field
// of a subset or a typed field Ichor owns, and wants an error that names it; a field the subset does carry must
// still convert.
func TestSubsetRefusesWhatItOmits(t *testing.T) {
	kubelet := func(image string, cfg map[string]any) config.Document {
		d := k8s.NewKubeletConfigV1Alpha1()
		d.KubeletImage = image
		d.KubeletConfig = mmeta.Unstructured{Object: cfg}

		return d
	}

	proxy := func(cfg map[string]any) config.Document {
		d := k8s.NewKubeProxyConfigV1Alpha1()
		d.ProxyConfig = mmeta.Unstructured{Object: cfg}

		return d
	}

	scheduler := func(cfg map[string]any) config.Document {
		d := k8s.NewKubeSchedulerConfigV1Alpha1()
		d.PodConfig = mmeta.Unstructured{Object: cfg}

		return d
	}

	enabled := true

	proxyEnabled := k8s.NewKubeProxyConfigV1Alpha1()
	proxyEnabled.ProxyEnabled = &enabled

	type object = map[string]any

	for name, tc := range map[string]struct {
		doc  config.Document
		want string // empty: the document converts
	}{
		"a kubelet field the subset carries": {kubelet("", object{"maxPods": 110}), ""},
		"a kubelet field Talos protects":     {kubelet("", object{"port": 10250}), `"port"`},
		"a kubelet field with no schema": {
			kubelet("", object{"logging": object{"format": "text"}}), `"logging"`,
		},
		"a kubelet field nobody is documented to set": {kubelet("", object{"enableServer": true}), `"enableServer"`},
		"a nested kubelet field the subset lacks": {
			kubelet("", object{"memorySwap": object{"nope": 1}}), `"nope"`,
		},
		"the kubelet image": {
			kubelet("ghcr.io/siderolabs/kubelet:v1.36.0", nil), "KubeletConfigV1Alpha1.image",
		},
		"a kube-proxy field the subset carries": {proxy(object{"clusterCIDR": "10.0.0.0/8"}), ""},
		"a kube-proxy field with no schema": {
			proxy(object{"clientConnection": object{"burst": 1}}), `"clientConnection"`,
		},
		"kube-proxy enabled": {proxyEnabled, "KubeProxyConfigV1Alpha1.enabled"},
		"a scheduler profile": {
			scheduler(object{"profiles": []any{object{"schedulerName": "x"}}}), "",
		},
		"a scheduler plugin's arguments": {
			scheduler(object{"profiles": []any{object{"pluginConfig": []any{}}}}), `"pluginConfig"`,
		},
		"a scheduler field with no schema": {
			scheduler(object{"leaderElection": object{}}), `"leaderElection"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ichorv1alpha1.TalosConfigPatchDocumentFromMachinery(tc.doc)

			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("the document converts, but: %v", err)
			case tc.want != "" && err == nil:
				t.Fatalf("a document that sets %s was accepted; want an error naming it", tc.want)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Fatalf("error %q does not name %s", err, tc.want)
			}
		})
	}
}
