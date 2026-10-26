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

func TestChangedPaths_NoDifference(t *testing.T) {
	cluster, m, bundle := diffTestFixtures(t, "v1.14")

	cfg, _, err := render.Render(cluster, m, bundle, nil, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	paths, err := ChangedPaths(cfg, cfg)
	if err != nil {
		t.Fatalf("changedPaths: %v", err)
	}

	if len(paths) != 0 {
		t.Fatalf("ChangedPaths(identical configs) = %v, want none", paths)
	}
}

func TestChangedPaths_LegacyFieldChange(t *testing.T) {
	cluster, m, bundle := diffTestFixtures(t, "v1.14")

	before, _, err := render.Render(cluster, m, bundle, nil, nil)
	if err != nil {
		t.Fatalf("Render(before): %v", err)
	}

	patch := ichorv1alpha1.TalosConfigPatch{
		Namespace: tmNamespace, Name: "p",
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      layerCluster,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: kindConfig, Config: extraArgsConfig()}},
		},
	}

	after, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
	if err != nil {
		t.Fatalf("Render(after): %v", err)
	}

	paths, err := ChangedPaths(before, after)
	if err != nil {
		t.Fatalf("changedPaths: %v", err)
	}

	// ExtraArgsConfig is a map, so the diff is reported per key rather than
	// at "extraArgs" itself -- fieldmeta's prefix match still covers it.
	if !slices.Contains(paths, "cluster.apiServer.extraArgs.foo") {
		t.Fatalf("changedPaths = %v, want it to contain cluster.apiServer.extraArgs.foo", paths)
	}
}

func TestChangedPaths_StandaloneDocumentAdded(t *testing.T) {
	cluster, m, bundle := diffTestFixtures(t, "v1.14")

	before, _, err := render.Render(cluster, m, bundle, nil, nil)
	if err != nil {
		t.Fatalf("Render(before): %v", err)
	}

	patch := ichorv1alpha1.TalosConfigPatch{
		Namespace: tmNamespace, Name: "p",
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      layerCluster,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind:         "SysctlConfig",
				SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.ipv4.ip_forward": "1"}},
			}},
		},
	}

	after, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
	if err != nil {
		t.Fatalf("Render(after): %v", err)
	}

	paths, err := ChangedPaths(before, after)
	if err != nil {
		t.Fatalf("changedPaths: %v", err)
	}

	if !slices.Contains(paths, "SysctlConfig.params.net.ipv4.ip_forward") {
		t.Fatalf("changedPaths = %v, want it to contain SysctlConfig.params.net.ipv4.ip_forward", paths)
	}
}

func TestChangedPaths_MalformedYAML(t *testing.T) {
	if _, err := ChangedPaths([]byte("{{{not valid yaml"), []byte("{{{not valid yaml")); err == nil {
		t.Fatal("ChangedPaths(malformed YAML) = nil error, want one")
	}
}
