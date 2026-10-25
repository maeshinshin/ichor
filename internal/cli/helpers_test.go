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
	"context"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// testNamespace is the namespace every fixture below lives in.
const testNamespace = "default"

// testSysctlKey is the one sysctl every fixture's SysctlConfig document
// sets, reused across every test that needs a non-secret, auto-patchable
// difference.
const testSysctlKey = "net.ipv4.ip_forward"

// fakeTalosReader is the talosConfigReader test double (unit rule 2: no
// call in these tests ever reaches a real Talos node).
type fakeTalosReader struct {
	current    []byte
	currentErr error
}

func (f *fakeTalosReader) CurrentConfig(context.Context) ([]byte, error) {
	return f.current, f.currentErr
}

// dialerFor returns a DialTalos func that hands back reader for any
// endpoint, or dialErr if set.
func dialerFor(reader talosConfigReader, dialErr error) func(context.Context, string) (talosConfigReader, error) {
	return func(context.Context, string) (talosConfigReader, error) {
		if dialErr != nil {
			return nil, dialErr
		}

		return reader, nil
	}
}

// testVersionContract is the version contract every fixture below renders
// against; it has to agree between the cluster and the bundle, so it is
// pulled out once rather than repeated at every call site.
const testVersionContract = "v1.14"

func newTestBundle(t *testing.T) *secrets.Bundle {
	t.Helper()

	contract, err := coreconfig.ParseContractFromVersion(testVersionContract)
	if err != nil {
		t.Fatalf("parsing version contract: %v", err)
	}

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	if err != nil {
		t.Fatalf("generating secrets bundle: %v", err)
	}

	return bundle
}

func newTestCluster() *ichorv1alpha1.TalosCluster {
	return &ichorv1alpha1.TalosCluster{
		Name: "adopt-cluster", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
			VersionContract:   testVersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "unused"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: "true"},
		},
	}
}

func newTestMachine(clusterName string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: "node-a", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-node-a"},
			Endpoint:   "10.0.0.10:50000",
			Role:       "worker",
			Approved:   true,
		},
	}
}

// sysctlPatch builds an unsubmitted "cluster"-layer SysctlConfig patch,
// used only to render a "current" fixture -- what a node adopted from
// outside Ichor already has -- never created through the fake client,
// since adopt's whole premise is that no TalosConfigPatch exists yet.
func sysctlPatch(clusterName string, params map[string]string) ichorv1alpha1.TalosConfigPatch {
	return ichorv1alpha1.TalosConfigPatch{
		Name: "fixture-sysctl", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      "cluster",
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind:         kindSysctlConfig,
				SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: params},
			}},
		},
	}
}

// legacyConfigPatch builds an unsubmitted "cluster"-layer legacy Config
// patch carrying a non-secret cluster.apiServer.extraArgs value, alongside
// the dummy (not bundle-derived) machine.token/type/certSANs the CRD's
// schema requires any Config document to carry. Used only to render a
// "current" fixture simulating drift adopt cannot safely auto-patch -- never created through the fake client.
func legacyConfigPatch(clusterName string) ichorv1alpha1.TalosConfigPatch {
	cfg := &typedconfig.Config{
		ConfigVersion: "v1alpha1",
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     "worker",
			MachineToken:    "328hom.uqjzh6jnn2eie9oi",
			MachineCertSANs: []string{"10.0.0.10"},
		},
		ClusterConfig: &typedconfig.ClusterConfig{
			APIServerConfig: &typedconfig.APIServerConfig{ExtraArgsConfig: map[string][]string{"foo": {"b"}}},
		},
	}

	return ichorv1alpha1.TalosConfigPatch{
		Name: "fixture-legacy", Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      "cluster",
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "Config", Config: cfg}},
		},
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s := runtime.NewScheme()
	if err := ichorv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("adding ichorv1alpha1 to scheme: %v", err)
	}

	return s
}

// bundleSecretMaterial lists every sensitive scalar in bundle, the same way
// pkg/render's and internal/controller's own leak tests do.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	return []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.ID,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Crt),
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Crt),
		string(bundle.Certs.K8s.Key),
	}
}
