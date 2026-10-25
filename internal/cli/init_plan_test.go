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
	"strings"
	"testing"
)

func validInitPlanInput() initPlanInput {
	return initPlanInput{
		Cluster:           "my-cluster",
		Namespace:         "default",
		Preset:            "standard",
		CNIPreset:         "cilium",
		EndpointType:      "vip",
		EndpointAddress:   "10.0.0.5",
		TalosVersion:      "v1.14.3",
		KubernetesVersion: "v1.33.0",
		VersionContract:   "v1.14",
		ControlPlaneCount: 1,
		Schedulable:       "true",
	}
}

func TestBuildInitPlan_Success(t *testing.T) {
	var warn bytes.Buffer

	cluster, err := buildInitPlan(validInitPlanInput(), &warn)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if warn.Len() != 0 {
		t.Fatalf("expected no warning for a vip endpoint, got: %s", warn.String())
	}

	if cluster.Name != "my-cluster" || cluster.Namespace != "default" {
		t.Fatalf("cluster name/namespace = %q/%q, want my-cluster/default", cluster.Name, cluster.Namespace)
	}

	if cluster.Spec.SecretsRef.Name != "my-cluster-secrets" {
		t.Fatalf("secretsRef.name = %q, want my-cluster-secrets", cluster.Spec.SecretsRef.Name)
	}

	if cluster.Spec.CNI.Preset != "cilium" || cluster.Spec.CNI.KubeProxy != "enabled" {
		t.Fatalf("cni = %+v, want preset cilium, kubeProxy enabled", cluster.Spec.CNI)
	}
}

func TestBuildInitPlan_NonVIPWarnsButSucceeds(t *testing.T) {
	in := validInitPlanInput()
	in.EndpointType = "node"

	var warn bytes.Buffer

	cluster, err := buildInitPlan(in, &warn)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Spec.Endpoint.Type != "node" {
		t.Fatalf("endpoint.type = %q, want node", cluster.Spec.Endpoint.Type)
	}

	if !strings.Contains(warn.String(), "vip") {
		t.Fatalf("expected a vip recommendation warning, got: %s", warn.String())
	}
}

func TestBuildInitPlan_Errors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*initPlanInput)
	}{
		{"unsupported preset", func(in *initPlanInput) { in.Preset = "minimal" }},
		{"invalid cni", func(in *initPlanInput) { in.CNIPreset = "calico" }},
		{"invalid endpoint type", func(in *initPlanInput) { in.EndpointType = "carrier-pigeon" }},
		{"empty endpoint address", func(in *initPlanInput) { in.EndpointAddress = "" }},
		{"invalid control plane count", func(in *initPlanInput) { in.ControlPlaneCount = 2 }},
		{"invalid schedulable", func(in *initPlanInput) { in.Schedulable = "maybe" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validInitPlanInput()
			tc.mutate(&in)

			var warn bytes.Buffer

			if _, err := buildInitPlan(in, &warn); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}
