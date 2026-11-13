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
	"io"
	"strings"
	"testing"
)

func validInitPlanInput() initPlanInput {
	return initPlanInput{
		Cluster:           testInitClusterName,
		Namespace:         ichorControllerNamespace,
		Preset:            defaultPreset,
		CNIPreset:         testCNICilium,
		EndpointType:      testEndpointTypeVIP,
		EndpointAddress:   testEndpointAddr,
		TalosVersion:      testTalosVersion,
		KubernetesVersion: "v1.33.0",
		VersionContract:   "v1.14",
		InstallDisk:       testInstallDisk,
	}
}

func TestBuildInitPlan_Success(t *testing.T) {
	var warn bytes.Buffer

	cluster, err := buildInitPlan(validInitPlanInput(), &warn)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if warn.Len() != 0 {
		t.Fatalf("expected no warning for a vip endpoint in the controller's own namespace, got: %s", warn.String())
	}

	if cluster.Name != testInitClusterName || cluster.Namespace != ichorControllerNamespace {
		t.Fatalf("cluster name/namespace = %q/%q, want %s/%s", cluster.Name, cluster.Namespace, testInitClusterName, ichorControllerNamespace)
	}

	if cluster.Spec.SecretsRef.Name != testInitClusterName+"-secrets" {
		t.Fatalf("secretsRef.name = %q, want my-cluster-secrets", cluster.Spec.SecretsRef.Name)
	}

	if cluster.Spec.CNI.Preset != testCNICilium || cluster.Spec.CNI.KubeProxy != kubeProxyEnabled {
		t.Fatalf("cni = %+v, want preset cilium, kubeProxy enabled", cluster.Spec.CNI)
	}

	if cluster.Spec.ControlPlane.Count != 3 || cluster.Spec.ControlPlane.Schedulable != schedulableFalse {
		t.Fatalf("controlPlane = %+v, want standard's count=3, schedulable=false", cluster.Spec.ControlPlane)
	}
}

// TestBuildInitPlan_PresetTopology proves each of the four presets sets
// the control-plane count and schedulable value architecture.md's
// "Nodes" section pairs it with.
func TestBuildInitPlan_PresetTopology(t *testing.T) {
	for preset, want := range presetTopology {
		t.Run(preset, func(t *testing.T) {
			in := validInitPlanInput()
			in.Preset = preset

			var warn bytes.Buffer

			cluster, err := buildInitPlan(in, &warn)
			if err != nil {
				t.Fatalf("buildInitPlan(%s): %v", preset, err)
			}

			if cluster.Spec.ControlPlane.Count != want.ControlPlaneCount || cluster.Spec.ControlPlane.Schedulable != want.Schedulable {
				t.Fatalf("buildInitPlan(%s) controlPlane = %+v, want %+v", preset, cluster.Spec.ControlPlane, want)
			}
		})
	}
}

// TestPresetTopology_NeverRejectsSchedulableWithNoWorkers is the
// structural check presetTopology's own doc comment promises: no preset
// in the table pairs schedulable=false with a topology that never adds
// workers (architecture.md's "Making control planes unschedulable with
// zero workers is rejected"). If this ever fails, buildInitPlan's own
// defensive check (TestBuildInitPlan_Errors' "would-be unschedulable
// preset with no workers" case) is the only thing stopping that preset
// from shipping a cluster that could never run a workload.
func TestPresetTopology_NeverRejectsSchedulableWithNoWorkers(t *testing.T) {
	for preset, top := range presetTopology {
		if top.Schedulable == schedulableFalse && !presetHasWorkers(preset) {
			t.Fatalf("preset %q is schedulable=false but presetHasWorkers says it never gets workers", preset)
		}
	}
}

func TestBuildInitPlan_NonVIPWarnsButSucceeds(t *testing.T) {
	in := validInitPlanInput()
	in.EndpointType = endpointNode

	var warn bytes.Buffer

	cluster, err := buildInitPlan(in, &warn)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Spec.Endpoint.Type != "node" {
		t.Fatalf("endpoint.type = %q, want node", cluster.Spec.Endpoint.Type)
	}

	if !strings.Contains(warn.String(), "VIP") {
		t.Fatalf("expected a vip recommendation warning, got: %s", warn.String())
	}
}

func TestBuildInitPlan_CNITunnel(t *testing.T) {
	for _, tunnel := range []string{"", ciliumTunnelVxlan, tunnelGeneve} {
		in := validInitPlanInput()
		in.CNITunnel = tunnel

		cluster, err := buildInitPlan(in, io.Discard)
		if err != nil {
			t.Fatalf("buildInitPlan with cilium and tunnel %q: %v", tunnel, err)
		}

		if got := cluster.Spec.CNI.Tunnel; got != tunnel {
			t.Errorf("spec.cni.tunnel = %q, want %q", got, tunnel)
		}
	}
}

func TestBuildInitPlan_Errors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*initPlanInput)
	}{
		{"cluster name is not a valid Kubernetes object name", func(in *initPlanInput) { in.Cluster = "../../../tmp/evil" }},
		{"unsupported preset", func(in *initPlanInput) { in.Preset = "minimal" }},
		{"invalid cni", func(in *initPlanInput) { in.CNIPreset = string(detectedCalico) }},
		{"flannel was removed", func(in *initPlanInput) { in.CNIPreset = "flannel" }},
		{"invalid cni tunnel", func(in *initPlanInput) { in.CNITunnel = "gre" }},
		{"invalid endpoint type", func(in *initPlanInput) { in.EndpointType = "carrier-pigeon" }},
		{"empty endpoint address", func(in *initPlanInput) { in.EndpointAddress = "" }},
		{"too many cert SANs", func(in *initPlanInput) { in.CertSANs = make([]string, maxCertSANs+1) }},
		{"empty cert SAN", func(in *initPlanInput) { in.CertSANs = []string{""} }},
		{"cert SAN longer than a DNS name", func(in *initPlanInput) {
			in.CertSANs = []string{strings.Repeat("a", maxCertSANLength+1)}
		}},
		{"pod subnet is not a CIDR", func(in *initPlanInput) { in.PodSubnets = []string{"10.248.0.0"} }},
		{"too many service subnets", func(in *initPlanInput) {
			in.ServiceSubnets = []string{"10.1.0.0/16", "fd00::/64", "10.2.0.0/16"}
		}},
		{"service subnet is not a CIDR", func(in *initPlanInput) { in.ServiceSubnets = []string{"nope"} }},
		{"discover address is not an address", func(in *initPlanInput) { in.DiscoverAddresses = []string{"nope"} }},
		{"discover address is IPv6", func(in *initPlanInput) { in.DiscoverAddresses = []string{"fd00::1"} }},
		{"discover CIDR is not a CIDR", func(in *initPlanInput) { in.DiscoverCIDRs = []string{"192.168.2.0"} }},
		{"discover CIDR is IPv6", func(in *initPlanInput) { in.DiscoverCIDRs = []string{"fd00::/120"} }},
		{"discover CIDR is wider than /24", func(in *initPlanInput) { in.DiscoverCIDRs = []string{"10.0.0.0/16"} }},
		{"too many discover CIDRs", func(in *initPlanInput) {
			in.DiscoverCIDRs = []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24"}
		}},
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

// TestBuildInitPlan_RejectsUnschedulableWithNoWorkers is the direct,
// reachable proof of architecture.md's "Making control planes
// unschedulable with zero workers is rejected": presetTopology's own
// four rows never produce this (every schedulable=false entry has
// presetHasWorkers true), so this calls buildInitPlan's actual
// enforcement point by adding a fifth, deliberately invalid entry for
// the duration of the test, standing in for a future preset that got
// the pairing wrong.
func TestBuildInitPlan_RejectsUnschedulableWithNoWorkers(t *testing.T) {
	const badPreset = "test-only-unschedulable-no-workers"

	presetTopology[badPreset] = topology{ControlPlaneCount: 1, Schedulable: schedulableFalse}
	t.Cleanup(func() { delete(presetTopology, badPreset) })

	in := validInitPlanInput()
	in.Preset = badPreset

	var warn bytes.Buffer

	if _, err := buildInitPlan(in, &warn); err == nil {
		t.Fatal("expected buildInitPlan to reject an unschedulable preset with no workers, got nil error")
	}
}

// TestBuildInitPlan_NonControllerNamespaceWarnsButSucceeds proves
// the namespace decision is enforced as a warning, not a
// rejection: --namespace can still differ from ichorControllerNamespace,
// but the operator is told why that is almost always wrong.
func TestBuildInitPlan_NonControllerNamespaceWarnsButSucceeds(t *testing.T) {
	in := validInitPlanInput()
	in.Namespace = testNamespace

	var warn bytes.Buffer

	cluster, err := buildInitPlan(in, &warn)
	if err != nil {
		t.Fatalf("buildInitPlan: %v", err)
	}

	if cluster.Namespace != testNamespace {
		t.Fatalf("cluster.Namespace = %q, want default", cluster.Namespace)
	}

	if !strings.Contains(warn.String(), ichorControllerNamespace) {
		t.Fatalf("expected a warning naming %q, got: %s", ichorControllerNamespace, warn.String())
	}
}
