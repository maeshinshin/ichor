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

package main

import (
	"fmt"
	"os"
	"strings"

	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	talosdoc "github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"

	"github.com/maeshinshin/ichor/pkg/release"
)

// podSubnet and serviceSubnet are the inner cluster's Kubernetes networks. They must not
// overlap the host cluster's own pod network, which is where the VM's address comes from:
// talos-cluster01 uses 10.244.0.0/16 (confirmed via `kubectl get nodes -o
// jsonpath={.spec.podCIDR}`), so these are pinned elsewhere (ADR 0007).
const (
	podSubnet     = "10.248.0.0/16"
	serviceSubnet = "10.249.0.0/16"
)

// localhostSANs lets the e2e suite reach the inner cluster's API server through a
// kubectl port-forward tunnel terminating on 127.0.0.1, which needs to be a valid SAN on the
// apiserver's serving certificate for TLS verification to pass (ADR 0007, Boundaries: a VM's
// pod IP is unreachable from the workstation running the suite).
var localhostSANs = []string{"127.0.0.1", "localhost"}

// generatedConfig is a single node's control-plane machine config, the admin talosconfig
// that authenticates against it, and the secrets bundle both were generated from.
type generatedConfig struct {
	machineConfig []byte
	// workerConfig is the matching worker config from the same input (same secrets bundle).
	workerConfig []byte
	talosconfig  *clientconfig.Config

	// secretsBundle is the same *secrets.Bundle the controller's loadBundle reads out
	// of a Secret (ADR 0032): publishHA writes it to disk in that exact shape so a
	// TalosCluster fixture pointed at the real nodes this config was rendered for can
	// dial their real Talos API with credentials that actually authenticate, instead
	// of a fresh, unrelated bundle that never would.
	secretsBundle *secrets.Bundle
}

// generateControlPlaneConfig builds a control-plane config for a cluster whose control
// planes are nodeIPs, using the Talos config generator - the same library talosctl gen
// config uses - rather than talosctl itself, so the endpoint and subnets can be set
// precisely through the documents it emits instead of patched into the legacy v1alpha1
// section afterwards (ADR 0007). The install disk/image match the Image Factory schematic
// and version the VM already booted from the ISO, so there is nothing left to resolve at
// install time.
//
// Talos control-plane configs carry no per-node fields, so the same rendered bytes apply
// to every control plane in nodeIPs unchanged - callers with more than one node apply this
// once per node, not once per node's own config. The cluster endpoint pins to nodeIPs[0]
// (this harness has no VIP/load balancer); WithEndpointList carries every node so Talos's
// own endpoint controllers, and talosctl, know about all of them.
func generateControlPlaneConfig(clusterName string, nodeIPs []string) (*generatedConfig, error) {
	endpoint := fmt.Sprintf("https://%s:6443", nodeIPs[0])
	installImage := fmt.Sprintf("factory.talos.dev/metal-installer/%s:%s", talosFactorySchematic, talosVersion)

	k8sVersion := strings.TrimPrefix(release.Pinned.Kubernetes, "v")
	if v := os.Getenv(k8sVersionEnv); v != "" {
		k8sVersion = strings.TrimPrefix(v, "v")
	}
	input, err := generate.NewInput(clusterName, endpoint, k8sVersion,
		generate.WithInstallDisk("/dev/vda"),
		generate.WithInstallImage(installImage),
		generate.WithEndpointList(nodeIPs),
		generate.WithAdditionalSubjectAltNames(localhostSANs),
	)
	if err != nil {
		return nil, fmt.Errorf("building config generator input: %w", err)
	}
	// Plain struct fields, not options: the generator has no With* for them.
	input.PodNet = []string{podSubnet}
	input.ServiceNet = []string{serviceSubnet}

	cfg, err := input.Config(machine.TypeControlPlane)
	if err != nil {
		return nil, fmt.Errorf("generating control-plane config: %w", err)
	}
	if err := assertEndpoint(cfg, endpoint); err != nil {
		return nil, err
	}
	if cfg, err = withoutFlannel(cfg); err != nil {
		return nil, err
	}

	machineConfig, err := cfg.Bytes()
	if err != nil {
		return nil, fmt.Errorf("marshaling control-plane config: %w", err)
	}

	wcfg, err := input.Config(machine.TypeWorker)
	if err != nil {
		return nil, fmt.Errorf("generating worker config: %w", err)
	}
	if wcfg, err = withoutFlannel(wcfg); err != nil {
		return nil, err
	}
	workerConfig, err := wcfg.Bytes()
	if err != nil {
		return nil, fmt.Errorf("marshaling worker config: %w", err)
	}

	talosCfg, err := input.Talosconfig()
	if err != nil {
		return nil, fmt.Errorf("generating talosconfig: %w", err)
	}

	return &generatedConfig{
		machineConfig: machineConfig, workerConfig: workerConfig,
		talosconfig: talosCfg, secretsBundle: input.Options.SecretsBundle,
	}, nil
}

// assertEndpoint fails loudly if cfg's Kubernetes cluster endpoint is not want. A previous
// attempt left it at talosctl's placeholder, which resolved through the pod gateway to the
// host cluster's own kube-apiserver and got dialled unprompted by Talos's endpoint
// controllers; every call was rejected by TLS verification, but it must never be left at a
// default (ADR 0007). This reads the endpoint back out of the documents the generator
// actually produced, not the input that asked for them.
func assertEndpoint(cfg coreconfig.Provider, want string) error {
	got := cfg.K8sClusterConfig().ClusterEndpoint()
	if got == nil || got.String() != want {
		return fmt.Errorf("refusing to use this config: cluster endpoint is %v, want %q (the VM's own address)", got, want)
	}
	return nil
}

// k8sVersionEnv overrides the Kubernetes version the inner cluster boots with (vX.Y.Z or X.Y.Z),
// so an upgrade run can start one patch or minor behind the newest release.
const k8sVersionEnv = "ICHOR_E2E_K8S_VERSION"

// workersEnv is how many worker VMs "up-ha" adds after the three control planes (default 0).
const workersEnv = "ICHOR_E2E_HA_WORKERS"

// withoutFlannel returns cfg with its KubeFlannelCNIConfig document removed.
func withoutFlannel(cfg coreconfig.Provider) (coreconfig.Provider, error) {
	var kept []talosdoc.Document
	for _, d := range cfg.Documents() {
		if d.Kind() == "KubeFlannelCNIConfig" {
			continue
		}
		kept = append(kept, d)
	}
	return container.New(kept...)
}
