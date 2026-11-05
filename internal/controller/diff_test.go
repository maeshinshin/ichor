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

package controller

import (
	"slices"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// These exercise changedPaths directly against pkg/render's real output, no
// envtest involved: a plain function given two []byte in, a []string out.

func diffTestFixtures(t *testing.T, versionContract string) (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion(versionContract)
	if err != nil {
		t.Fatalf("ParseContractFromVersion(%q): %v", versionContract, err)
	}

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}

	cluster := &ichorv1alpha1.TalosCluster{
		Namespace: tmNamespace, Name: "c",
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: constants.DefaultKubernetesVersion,
			VersionContract:   versionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "bundle"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
		},
	}

	m := &ichorv1alpha1.TalosMachine{
		Namespace: tmNamespace, Name: "m1",
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-m1"},
			Endpoint:   "10.0.0.10:50000",
			Role:       roleWorker,
		},
	}

	return cluster, m, bundle
}

func TestChangedPaths_PatchedConfig(t *testing.T) {
	cluster, m, bundle := diffTestFixtures(t, "v1.14")

	renderWith := func(doc *ichorv1alpha1.TalosConfigPatchDocument) []byte {
		t.Helper()

		var patches []ichorv1alpha1.TalosConfigPatch
		if doc != nil {
			patches = []ichorv1alpha1.TalosConfigPatch{{
				Namespace: tmNamespace, Name: "p",
				Spec: ichorv1alpha1.TalosConfigPatchSpec{
					ClusterRef: corev1.LocalObjectReference{Name: "c"}, Layer: layerCluster,
					Documents: []ichorv1alpha1.TalosConfigPatchDocument{*doc},
				},
			}}
		}

		cfg, _, err := render.Render(cluster, m, bundle, patches, nil)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		return cfg
	}

	before := renderWith(nil)

	for name, tc := range map[string]struct {
		doc  *ichorv1alpha1.TalosConfigPatchDocument
		want string // "" means no difference
	}{
		"no difference": {nil, ""},
		// ExtraArgsConfig is a map, so the diff is reported per key rather than at "extraArgs"
		// itself -- fieldmeta's prefix match still covers it.
		"legacy field change": {
			&ichorv1alpha1.TalosConfigPatchDocument{Kind: kindConfig, Config: extraArgsConfig()},
			"cluster.apiServer.extraArgs.foo",
		},
		"standalone document added": {
			&ichorv1alpha1.TalosConfigPatchDocument{
				Kind:         "SysctlConfig",
				SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.ipv4.ip_forward": "1"}},
			},
			"SysctlConfig.params.net.ipv4.ip_forward",
		},
	} {
		paths, err := ChangedPaths(before, renderWith(tc.doc))
		if err != nil {
			t.Fatalf("%s: ChangedPaths: %v", name, err)
		}

		if tc.want == "" && len(paths) != 0 || tc.want != "" && !slices.Contains(paths, tc.want) {
			t.Fatalf("%s: ChangedPaths = %v, want %q", name, paths, tc.want)
		}
	}
}

func TestChangedPaths_MalformedYAML(t *testing.T) {
	if _, err := ChangedPaths([]byte("{{{not valid yaml"), []byte("{{{not valid yaml")); err == nil {
		t.Fatal("ChangedPaths(malformed YAML) = nil error, want one")
	}
}

const kernelModuleDoc = "apiVersion: v1alpha1\nkind: KernelModuleConfig\nname: br_netfilter\n"

func TestCollapseDocumentPaths(t *testing.T) {
	const worker = "version: v1alpha1\nmachine:\n  type: worker\n"

	for name, tc := range map[string]struct {
		current, rendered string
		want              []string
	}{
		"an added document is reported as the document": {
			worker, worker + "---\n" + kernelModuleDoc, []string{"KernelModuleConfig br_netfilter (added)"},
		},
		"a removed document and untouched fields": {
			worker + "---\n" + kernelModuleDoc, "version: v1alpha1\nmachine:\n  type: controlplane\n",
			[]string{"KernelModuleConfig br_netfilter (removed)", "machine.type"},
		},
	} {
		current, rendered := []byte(tc.current), []byte(tc.rendered)

		paths, err := ChangedPaths(current, rendered)
		if err != nil {
			t.Fatalf("%s: ChangedPaths: %v", name, err)
		}

		got, err := collapseDocumentPaths(current, rendered, paths)
		if err != nil {
			t.Fatalf("%s: collapseDocumentPaths: %v", name, err)
		}

		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: collapseDocumentPaths = %v, want %v (raw paths %v)", name, got, tc.want, paths)
		}
	}
}
