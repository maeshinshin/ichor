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
	"sync"
	"testing"
	"time"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/release"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// testNamespace is the namespace every fixture below lives in.
const (
	testNamespace   = "default"
	testInstallDisk = "/dev/sda"
)

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

// Fixture literals shared across adopt's and init's test files.
const (
	testEndpointTypeVIP = "vip"
	testEndpointAddr    = "10.0.0.5"
	testTalosVersion    = "v1.14.5"
	testSchedulableTrue = "true"
	testInitMachineUUID = "uuid-node-a"
	testInitClusterName = "my-cluster"
	testCNICilium       = "cilium"

	// testReleasedVersion is a semantic version, so init creates an IchorInstallation.
	testReleasedVersion = "v0.3.1"

	// testEphemeralDiskSelector and testStorageDiskSelector are disk-role
	// CEL selector fixtures shared across disk-role tests.
	testEphemeralDiskSelector = `disk.wwid == "eph"`
	testStorageDiskSelector   = `disk.wwid == "store"`

	// testRoleWorker is the machine role fixture shared across tests.
	testRoleWorker = "worker"

	// testAPIVersion is the ichorv1alpha1 group/version string, shared by
	// every fixture that needs to name it literally (an unstructured
	// object, a managedFields entry) rather than through the typed API.
	testAPIVersion = "ichor.maesh.dev/v1alpha1"
)

var (
	sharedBundleOnce sync.Once
	sharedBundle     *secrets.Bundle
)

// newTestBundle returns one shared, read-only bundle: generating one costs a 4096-bit RSA key. A
// test that needs a bundle distinct from another calls newFreshTestBundle.
func newTestBundle(t *testing.T) *secrets.Bundle {
	t.Helper()

	sharedBundleOnce.Do(func() { sharedBundle = newFreshTestBundle(t) })

	return sharedBundle
}

func newFreshTestBundle(t *testing.T) *secrets.Bundle {
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
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointTypeVIP, Address: testEndpointAddr},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: release.Pinned.Kubernetes,
			VersionContract:   testVersionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: "unused"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulableTrue},
		},
	}
}

func newTestMachine(clusterName string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		Name: testMachineName, Namespace: testNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: testInitMachineUUID},
			Endpoint:      "10.0.0.10:50000",
			Role:          testRoleWorker,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
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
			Layer:      cniPatchLayer,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind:         kindSysctlConfig,
				SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: params},
			}},
		},
	}
}

// legacyConfigPatch builds an unsubmitted "cluster"-layer legacy Config
// patch carrying a non-secret cluster.apiServer.extraArgs value. It used to
// carry a dummy machine.token too, because the mirror schema made that field
// required on any Config document; machine.token (and
// eight other secret-bearing fields) has since been dropped from the schema entirely, so a sparse
// Config document is now expressible. Used only to render a "current" fixture
// simulating drift adopt cannot safely auto-patch -- never created
// through the fake client. Whether that premise still holds now that sparse
// capture is possible is the to revisit.
func legacyConfigPatch(clusterName string) ichorv1alpha1.TalosConfigPatch {
	cfg := &typedconfig.Config{
		ConfigVersion: "v1alpha1",
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     testRoleWorker,
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
			Layer:      cniPatchLayer,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: "Config", Config: cfg}},
		},
	}
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	s, err := cliScheme()
	if err != nil {
		t.Fatalf("building the CLI scheme: %v", err)
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

// fakeMaintenanceApplier is the maintenanceApplier test double (unit rule
// 2: no call in these tests ever reaches a real Talos node).
type fakeMaintenanceApplier struct {
	applied   []byte
	applyErr  error
	callCount int
}

func (f *fakeMaintenanceApplier) ApplyInsecure(_ context.Context, cfg []byte) error {
	f.callCount++
	f.applied = cfg

	return f.applyErr
}

// fakeBootstrapClient is the bootstrapClient test double.
type fakeBootstrapClient struct {
	bootstrapCalled bool
	bootstrapErr    error
	kubeconfig      []byte
	kubeconfigErr   error
}

func (f *fakeBootstrapClient) Bootstrap(context.Context) error {
	f.bootstrapCalled = true

	return f.bootstrapErr
}

func (f *fakeBootstrapClient) Kubeconfig(context.Context) ([]byte, error) {
	return f.kubeconfig, f.kubeconfigErr
}

// validInitOptions returns an initOptions that passes buildInitPlan and
// drives runInit end to end against a cilium CNI, so tests get a
// TalosConfigPatch to assert on alongside the TalosCluster/TalosMachine.
// validInitOptions has no sign-in: most init tests are about the node. The default, sign-in on, is
// the one tests set explicitly (AllowUnauthenticated = false) or get from initOptsWithFlags.
func validInitOptions() initOptions {
	return initOptions{
		AllowUnauthenticated: true,
		BundlePlaintext:      true,
		Cluster:              testInitClusterName,
		Namespace:            testNamespace,
		Preset:               defaultPreset,
		CNIPreset:            testCNICilium,
		EndpointType:         testEndpointTypeVIP,
		EndpointAddress:      testEndpointAddr,
		TalosVersion:         testTalosVersion,
		KubernetesVersion:    "v1.33.0",
		VersionContract:      testVersionContract,
		InstallDisk:          testInstallDisk,
		NodeEndpoint:         "10.0.0.10:50000",
	}
}

// testIchorManifest stands in for installmanifest.Manifest in tests: a
// real (if tiny), decodable, non-placeholder multi-document manifest, so
// applyIchorManifest's own logic -- decode, create each object, refuse
// the placeholder -- is exercised the same way the real one would be,
// without this test package needing the installembed build tag.
const testIchorManifest = `apiVersion: v1
kind: Namespace
metadata:
  name: ichor-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ichor-controller
  namespace: ichor-system
`

// testInitLink is the one physical link the fake first node reports.
var testInitLink = ichorv1alpha1.TalosMachineDiscoveredLink{Name: testLinkName, PermanentMAC: testLinkMAC, LinkUp: true}

// newFakeInitDeps builds an initDeps wired to fakes: identity always
// succeeds with a fixed UUID, maintenance apply and bootstrap are
// recorded on the returned fakes, and NewK8sClient always returns the
// same fake controller-runtime client regardless of the kubeconfig bytes
// handed to it.
func newFakeInitDeps(t *testing.T, k8sClient client.Client) (
	initDeps, *fakeMaintenanceApplier, *fakeBootstrapClient,
) {
	t.Helper()

	maint := &fakeMaintenanceApplier{}
	boot := &fakeBootstrapClient{kubeconfig: []byte("apiVersion: v1\nkind: Config\n")}

	deps := initDeps{
		ReadNode: func(context.Context, string, bool) (nodeReading, error) {
			return nodeReading{
				Identity: controller.HardwareIdentity{UUID: testInitMachineUUID, Serial: "serial-1"},
				Links:    []ichorv1alpha1.TalosMachineDiscoveredLink{testInitLink},
			}, nil
		},
		DialMaintenance: func(context.Context, string) (maintenanceApplier, error) { return maint, nil },
		DialAdmin: func(context.Context, *ichorv1alpha1.TalosCluster, string, *secrets.Bundle) (bootstrapClient, error) {
			return boot, nil
		},
		NewK8sClient:  func([]byte) (client.Client, error) { return k8sClient, nil },
		IchorManifest: []byte(testIchorManifest),
		Stdout:        &bytes.Buffer{},
		Stderr:        &bytes.Buffer{},
		Stdin:         strings.NewReader(""),
		AssumeYes:     true,
		Output:        outputYAML,
	}

	return deps, maint, boot
}
