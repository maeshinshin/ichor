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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
)

const (
	writeUUIDCP     = "11111111-1111-1111-1111-111111111111"
	writeUUIDWorker = "22222222-2222-2222-2222-222222222222"
	writeAddrCP     = "10.0.0.11"
	writeAddrWorker = "10.0.0.12"
)

func writeIdentity(_ context.Context, address string) (controller.HardwareIdentity, error) {
	switch address {
	case writeAddrCP:
		return controller.HardwareIdentity{UUID: writeUUIDCP, Serial: "S1", MACAddresses: []string{"aa:bb:cc:00:00:01"}}, nil
	case writeAddrWorker:
		return controller.HardwareIdentity{UUID: writeUUIDWorker, Serial: "S2", MACAddresses: []string{"aa:bb:cc:00:00:02"}}, nil
	default:
		return controller.HardwareIdentity{}, errors.New("no such node")
	}
}

func fullWriteInput(t *testing.T) adoptWriteInput {
	t.Helper()

	return adoptWriteInput{
		Namespace: testNamespace,
		Bundle:    newTestBundle(t),
		Cluster: &adoptWriteCluster{
			Name:         testInitClusterName,
			Endpoint:     ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointTypeVIP, Address: testEndpointAddr},
			TalosVersion: testTalosVersion, KubernetesVersion: "v1.34.0", VersionContract: testVersionContract,
			PodSubnets: []string{"10.244.0.0/16"}, ServiceSubnets: []string{"10.96.0.0/12"},
			ControlPlaneCount: 1, Schedulable: testSchedulableTrue,
			CNIPreset: testCNICilium, KubeProxy: kubeProxyReplaced,
		},
		Machines: []adoptWriteMachine{
			{Address: writeAddrCP, Role: roleControlPlane, InstallDisk: testInstallDisk},
			{Address: writeAddrWorker, Role: testRoleWorker, InstallDisk: testInstallDisk},
		},
		Patches: []ichorv1alpha1.TalosConfigPatch{
			sysctlPatch(testInitClusterName, map[string]string{testSysctlKey: "1"}),
		},
		ReadIdentity: writeIdentity,
		Stderr:       &bytes.Buffer{},
	}
}

func newWriteFake(t *testing.T) client.Client {
	t.Helper()

	return fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
}

func TestWriteAdopt_WritesEveryObject(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)

	res, err := writeAdopt(ctx, c, in)
	if err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	if len(res.Created) != 5 || len(res.Unchanged) != 0 {
		t.Fatalf("created %v, unchanged %v; want 5 created (Secret, cluster, 2 machines, patch)", res.Created, res.Unchanged)
	}

	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: secretsRefName(testInitClusterName)}, &sec); err != nil {
		t.Fatalf("bundle Secret: %v", err)
	}

	if len(sec.Data[secretsbundle.SecretKey]) == 0 {
		t.Fatal("bundle Secret has no bundle")
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatalf("TalosCluster: %v", err)
	}

	if cluster.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] != annotationTrue {
		t.Errorf("adopted annotation missing: %v", cluster.Annotations)
	}

	spec := cluster.Spec
	if spec.VersionContract != testVersionContract || spec.CNI.Preset != testCNICilium || spec.CNI.KubeProxy != kubeProxyReplaced ||
		spec.Network == nil || spec.Network.PodSubnets[0] != "10.244.0.0/16" || spec.Network.ServiceSubnets[0] != "10.96.0.0/12" ||
		spec.CNI.Unmanaged != nil {
		t.Errorf("unexpected cluster spec: %+v", spec)
	}

	for uuid, role := range map[string]string{writeUUIDCP: roleControlPlane, writeUUIDWorker: testRoleWorker} {
		var m ichorv1alpha1.TalosMachine
		if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: uuid}, &m); err != nil {
			t.Fatalf("TalosMachine %s: %v", uuid, err)
		}

		if m.Annotations[ichorv1alpha1.TalosMachineHoldApplyAnnotation] != annotationTrue {
			t.Errorf("machine %s is not held: %v", uuid, m.Annotations)
		}

		if m.Spec.Role != role || m.Spec.Install == nil || m.Spec.Identity.UUID != uuid || m.Spec.Identity.Serial == "" ||
			len(m.Spec.Identity.MACAddresses) != 1 || m.Spec.ClusterRef.Name != testInitClusterName {
			t.Errorf("unexpected machine spec for %s: %+v", uuid, m.Spec)
		}
	}

	var patch ichorv1alpha1.TalosConfigPatch
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "fixture-sysctl"}, &patch); err != nil {
		t.Fatalf("TalosConfigPatch: %v", err)
	}
}

func TestWriteAdopt_UnmanagedClusterRecordsTalosCNI(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)
	in.Cluster.CNIPreset, in.Cluster.TalosCNI, in.Cluster.KubeProxy = ichorv1alpha1.TalosClusterCNIPresetUnmanaged, string(detectedNone), cniTestEnabled

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	var cluster ichorv1alpha1.TalosCluster
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testInitClusterName}, &cluster); err != nil {
		t.Fatal(err)
	}

	if cluster.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged || cluster.Spec.CNI.Unmanaged == nil ||
		cluster.Spec.CNI.Unmanaged.TalosCNI != "none" || cluster.Spec.CNI.KubeProxy != cniTestEnabled {
		t.Errorf("unexpected CNI: %+v", cluster.Spec.CNI)
	}

	// A second run with the same facts still writes nothing.
	res, err := writeAdopt(ctx, c, in)
	if err != nil || len(res.Created) != 0 {
		t.Fatalf("second run: created %v, err %v", res.Created, err)
	}
}

// writeDenyPolicy is the default-deny policy testPolicies declares.
const writeDenyPolicy = "ichor-default-deny"

// getDenyPolicy reads Ichor's default-deny CiliumNetworkPolicy; the error is nil when it exists.
func getDenyPolicy(ctx context.Context, c client.Reader) error {
	pol := &unstructured.Unstructured{}
	pol.SetAPIVersion("cilium.io/v2")
	pol.SetKind("CiliumNetworkPolicy")

	return c.Get(ctx, client.ObjectKey{Namespace: ichorControllerNamespace, Name: writeDenyPolicy}, pol)
}

// ADR 0104 2.1 (D3): the preset does not veto the gate, so an unmanaged cluster that runs Cilium
// gets Ichor's policies.
func TestWriteAdopt_UnmanagedCiliumAppliesNetworkPolicies(t *testing.T) {
	setVersion(t, testReleasedVersion)

	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(ciliumDaemonSetObj(1, 1), ciliumCRDObj(true)).Build()
	in := fullWriteInput(t)
	in.Cluster.CNIPreset, in.Cluster.TalosCNI, in.Cluster.KubeProxy = ichorv1alpha1.TalosClusterCNIPresetUnmanaged, string(detectedNone), cniTestEnabled
	in.Ichor = &adoptWriteIchor{
		Manifest: []byte(testIchorManifest), NetworkPolicies: []byte(testPolicies),
		DetectedCNI: string(detectedCilium), PolicyCRDPresent: true, CiliumWait: time.Second,
	}
	in.Interval = time.Millisecond

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	if err := getDenyPolicy(ctx, c); err != nil {
		t.Fatalf("Ichor's network policy was not applied: %v", err)
	}
}

// ADR 0104 2.1: a non-Cilium CNI skips the policies and the message names the detected CNI.
func TestWriteAdopt_UnmanagedFlannelSkipsNetworkPoliciesAndNamesIt(t *testing.T) {
	setVersion(t, testReleasedVersion)

	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)
	in.Cluster.CNIPreset, in.Cluster.TalosCNI, in.Cluster.KubeProxy = ichorv1alpha1.TalosClusterCNIPresetUnmanaged, string(detectedNone), cniTestEnabled

	var out bytes.Buffer

	in.Stderr = &out
	in.Ichor = &adoptWriteIchor{
		Manifest: []byte(testIchorManifest), NetworkPolicies: []byte(testPolicies),
		DetectedCNI: string(detectedFlannel), PolicyCRDPresent: false,
	}
	in.Interval = time.Millisecond

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("writeAdopt: %v", err)
	}

	if !strings.Contains(out.String(), "flannel") {
		t.Errorf("skip message does not name the detected CNI:\n%s", out.String())
	}

	if err := getDenyPolicy(ctx, c); err == nil {
		t.Errorf("a network policy was applied on a flannel cluster")
	}
}

func TestWriteAdopt_SecondRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("first run: %v", err)
	}

	res, err := writeAdopt(ctx, c, in)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if len(res.Created) != 0 || len(res.Unchanged) != 5 {
		t.Fatalf("second run created %v, unchanged %v; want nothing created", res.Created, res.Unchanged)
	}
}

func TestWriteAdopt_ReleasedHoldIsNotAConflict(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatal(err)
	}

	var m ichorv1alpha1.TalosMachine
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: writeUUIDCP}, &m); err != nil {
		t.Fatal(err)
	}

	delete(m.Annotations, ichorv1alpha1.TalosMachineHoldApplyAnnotation)

	if err := c.Update(ctx, &m); err != nil {
		t.Fatal(err)
	}

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("rerun after the hold was released: %v", err)
	}
}

func TestWriteAdopt_RefusesADifferingObject(t *testing.T) {

	tests := map[string]func(t *testing.T, c client.Client){
		"secret": func(t *testing.T, c client.Client) {
			t.Helper()
			mustCreate(t, c, &corev1.Secret{
				Name: secretsRefName(testInitClusterName), Namespace: testNamespace,
				Data: map[string][]byte{secretsbundle.SecretKey: []byte("another bundle")},
			})
		},
		"TalosCluster": func(t *testing.T, c client.Client) {
			t.Helper()
			other := newTestCluster()
			other.Name, other.Namespace = testInitClusterName, testNamespace
			other.Spec.VersionContract = "v1.12"
			mustCreate(t, c, other)
		},
		"machine": func(t *testing.T, c client.Client) {
			t.Helper()
			m := newTestMachine(testInitClusterName)
			m.Name, m.Namespace = writeUUIDCP, testNamespace
			m.Spec.Role = testRoleWorker
			m.Spec.Identity.UUID = writeUUIDCP
			mustCreate(t, c, m)
		},
		"patch": func(t *testing.T, c client.Client) {
			t.Helper()
			p := sysctlPatch(testInitClusterName, map[string]string{testSysctlKey: "0"})
			mustCreate(t, c, &p)
		},
	}

	for name, seed := range tests {
		t.Run(name, func(t *testing.T) {
			c := newWriteFake(t)
			seed(t, c)

			in := fullWriteInput(t)

			_, err := writeAdopt(context.Background(), c, in)
			if !errors.Is(err, errAdoptDiffers) {
				t.Fatalf("got %v, want errAdoptDiffers", err)
			}

			assertNoMaterial(t, err.Error())
		})
	}
}

func mustCreate(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()

	if err := c.Create(context.Background(), obj); err != nil {
		t.Fatalf("seeding %T: %v", obj, err)
	}
}

func assertNoMaterial(t *testing.T, text string) {
	t.Helper()

	b := newTestBundle(t)

	for _, secret := range bundleSecretMaterial(b) {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("secret material leaked into %.60q...", text)
		}
	}
}

func TestWriteAdopt_NoSecretMaterialOutsideTheSecret(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)
	stderr := &bytes.Buffer{}
	in.Stderr = stderr

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatal(err)
	}

	var clusters ichorv1alpha1.TalosClusterList
	var machines ichorv1alpha1.TalosMachineList
	var patches ichorv1alpha1.TalosConfigPatchList

	for _, l := range []client.ObjectList{&clusters, &machines, &patches} {
		if err := c.List(ctx, l); err != nil {
			t.Fatal(err)
		}

		raw, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}

		assertNoMaterial(t, string(raw))
	}

	assertNoMaterial(t, stderr.String())
}

func TestWriteAdopt_PatchesOnlyKeepsTodaysBehaviour(t *testing.T) {
	ctx := context.Background()
	c := newWriteFake(t)

	res, err := writeAdopt(ctx, c, adoptWriteInput{
		Namespace: testNamespace,
		Patches:   []ichorv1alpha1.TalosConfigPatch{sysctlPatch(testInitClusterName, map[string]string{testSysctlKey: "1"})},
	})
	if err != nil || len(res.Created) != 1 {
		t.Fatalf("created %v, err %v; want exactly the patch", res, err)
	}

	var secrets corev1.SecretList
	if err := c.List(ctx, &secrets); err != nil || len(secrets.Items) != 0 {
		t.Fatalf("a Secret was written: %v %v", secrets.Items, err)
	}
}

func TestWriteAdopt_MachineNeedsAStrongIdentity(t *testing.T) {
	c := newWriteFake(t)
	in := fullWriteInput(t)
	in.ReadIdentity = func(context.Context, string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{}, nil
	}

	if _, err := writeAdopt(context.Background(), c, in); err == nil || !strings.Contains(err.Error(), "SMBIOS UUID") {
		t.Fatalf("got %v, want a refusal naming the missing UUID", err)
	}
}

func TestWriteAdopt_InstallsIchorOnlyWhenItIsNotRunning(t *testing.T) {
	setVersion(t, testReleasedVersion)

	ctx := context.Background()
	c := newWriteFake(t)
	in := fullWriteInput(t)
	in.Ichor = &adoptWriteIchor{Manifest: []byte(testIchorManifest)}
	in.Interval = time.Millisecond

	if _, err := writeAdopt(ctx, c, in); err != nil {
		t.Fatalf("first run: %v", err)
	}

	var ns corev1.Namespace
	if err := c.Get(ctx, types.NamespacedName{Name: ichorControllerNamespace}, &ns); err != nil {
		t.Fatalf("Ichor's namespace was not installed: %v", err)
	}

	var inst ichorv1alpha1.IchorInstallation
	if err := c.Get(ctx, types.NamespacedName{Name: ichorv1alpha1.IchorInstallationName}, &inst); err != nil {
		t.Fatalf("IchorInstallation: %v", err)
	}

	// Running now: a second run neither fails on a manifest it does not even look at nor reinstalls.
	in.Ichor = &adoptWriteIchor{Manifest: []byte("not a manifest: [")}

	res, err := writeAdopt(ctx, c, in)
	if err != nil || len(res.Created) != 0 {
		t.Fatalf("second run: created %v, err %v", res.Created, err)
	}
}
