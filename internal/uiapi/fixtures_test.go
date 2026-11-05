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

package uiapi

import (
	"sync"
	"time"

	. "github.com/onsi/gomega"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// Fixture helpers below mirror internal/controller's own
// (talosmachine_controller_test.go) in shape -- same envtest conventions,
// same namespace -- but are this package's own copies: they are
// unexported test helpers in a different package, so there is nothing to
// import.

const testNamespace = "default"

const (
	roleWorker       = "worker"
	roleControlPlane = "controlplane"
	layerCluster     = "cluster"
	kindConfig       = "Config"
	testMAC          = "aa:bb:cc:dd:ee:ff"
	// testVersionContract is the one Talos version contract every fixture in
	// this package renders against; nothing here exercises version-contract
	// gating itself (pkg/fieldmeta and pkg/render's own tests already do).
	testVersionContract = "v1.14"
	// fixtureTalos and fixtureK8s are the running versions the fixtures' clusters and nodes report.
	fixtureTalos = "v1.14.3"
	fixtureK8s   = "v1.36.2"
)

// newTestBundle returns one shared, read-only bundle: generating one costs a 4096-bit RSA key.
var newTestBundle = sync.OnceValue(newFreshTestBundle)

func newFreshTestBundle() *secrets.Bundle {
	contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
	Expect(err).NotTo(HaveOccurred())

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	Expect(err).NotTo(HaveOccurred())

	return bundle
}

func newTestSecret(bundle *secrets.Bundle) *corev1.Secret {
	raw, err := yaml.Marshal(bundle)
	Expect(err).NotTo(HaveOccurred())

	secret := &corev1.Secret{
		GenerateName: "bundle-", Namespace: testNamespace,
		Data: map[string][]byte{secretsbundle.SecretKey: raw},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	return secret
}

// newTestClusterWithBundle is newTestCluster plus a real secrets bundle
// Secret, for any test whose path reaches loadBundle -- dialing a NodeTalosClient
// for an approved machine, or rendering the effective configuration.
func newTestClusterWithBundle() *ichorv1alpha1.TalosCluster {
	secret := newTestSecret(newTestBundle())

	return newTestCluster(secret.Name)
}

func newTestCluster(secretName string) *ichorv1alpha1.TalosCluster {
	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: "cluster-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      fixtureTalos,
			KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
			VersionContract:   testVersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretName},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: "true"},
		},
	}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	return cluster
}

// strongTestIdentity is a complete identity tuple (docs/adr/0011): UUID,
// serial and at least one permanent MAC.
func strongTestIdentity(uuid string) ichorv1alpha1.TalosMachineIdentity {
	return ichorv1alpha1.TalosMachineIdentity{UUID: uuid, Serial: "serial-" + uuid, MACAddresses: []string{testMAC}}
}

func newTestMachine(clusterName string, identity ichorv1alpha1.TalosMachineIdentity, role, approvalState string) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		GenerateName: "machine-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      identity,
			Endpoint:      "10.0.0.10:50000",
			Role:          role,
			ApprovalState: approvalState,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

func minimalConfigSkeleton(role string) *typedconfig.Config {
	return &typedconfig.Config{
		ConfigVersion: "v1alpha1",
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     role,
			MachineCertSANs: []string{"10.0.0.10"},
		},
		ClusterConfig: &typedconfig.ClusterConfig{},
	}
}

// extraArgsConfig is minimalConfigSkeleton with one extra, cluster-layer
// API server argument set -- the fixture
// internal/controller/talosmachine_controller_test.go uses for the same
// purpose, under the same name.
func extraArgsConfig(role string) *typedconfig.Config {
	cfg := minimalConfigSkeleton(role)
	cfg.ClusterConfig.APIServerConfig = &typedconfig.APIServerConfig{ExtraArgsConfig: map[string][]string{"foo": {"b"}}}

	return cfg
}

func newTestPatch(clusterName, layer string, target *ichorv1alpha1.TalosConfigPatchTarget, priority int32, cfg *typedconfig.Config) *ichorv1alpha1.TalosConfigPatch {
	p := &ichorv1alpha1.TalosConfigPatch{
		GenerateName: "patch-", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      layer,
			Priority:   priority,
			Target:     target,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: kindConfig, Config: cfg}},
		},
	}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())

	return p
}
