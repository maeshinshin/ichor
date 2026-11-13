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
	"errors"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/pkg/render"
)

// `ichor adopt --add-machine` registers a configured machine that already carries an
// Ichor cluster's CAs. These tests use the flow environment of a cluster of one control plane and
// one worker (adopt_flow_test.go) and add a third node whose live configuration is a render.

const (
	addAddr         = "10.0.0.12"
	testReadyReason = "Test"
)

// newcomer makes addr answer with a configuration rendered for a machine of role, signed by bundle.
func (e *flowEnv) newcomer(t *testing.T, addr, role string, bundle *secrets.Bundle, patches ...ichorv1alpha1.TalosConfigPatch) {
	t.Helper()

	m := newTestMachine(e.cluster.Name)
	m.Name, m.Spec.Role, m.Spec.Endpoint = "uuid-"+addr, role, addr

	cfg, _, err := render.Render(e.cluster, m, bundle, patches, nil)
	if err != nil {
		t.Fatalf("rendering the live configuration of %s: %v", addr, err)
	}

	e.live[addr] = cfg
}

// addDeps are the deps of an --add-machine run for addAddr. The stand-in controller of the test marks
// every TalosMachine Ready, which is what the release waits for.
func (e *flowEnv) addDeps(t *testing.T) adoptDeps {
	t.Helper()

	e.afterGet = func(o client.Object) {
		if m, ok := o.(*ichorv1alpha1.TalosMachine); ok {
			m.Status.Conditions = []metav1.Condition{{
				Type: conditionReady, Status: metav1.ConditionTrue, Reason: testReadyReason, LastTransitionTime: metav1.Now(),
			}}
		}
	}

	d := e.deps(t)
	d.AssumeYes = true
	d.AddMachines = []string{addAddr}
	d.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "uuid-" + address, Serial: "s"}, nil
	}

	return d
}

func (e *flowEnv) machine(t *testing.T, name string) (*ichorv1alpha1.TalosMachine, bool) {
	t.Helper()

	var m ichorv1alpha1.TalosMachine

	if err := e.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, &m); err != nil {
		return nil, false
	}

	return &m, true
}

func newUnmanagedAddEnv(t *testing.T) *flowEnv {
	t.Helper()

	return newFlowEnv(t, ichorv1alpha1.TalosClusterCNIPresetUnmanaged, nil)
}

// A worker in the cluster's PKI with a configuration that already equals the render is registered
// approved and held, and released once the live check passes.
func TestAdoptAddMachineRegistersAndReleases(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t))

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, env.addDeps(t)); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	m, ok := env.machine(t, "uuid-"+addAddr)
	if !ok {
		t.Fatalf("the TalosMachine was not created\n%s", env.stderr.String())
	}

	if m.Spec.Role != testRoleWorker || m.Spec.Endpoint != addAddr || !m.Spec.IsApproved() || m.Spec.ClusterRef.Name != env.cluster.Name {
		t.Fatalf("unexpected machine spec: %+v", m.Spec)
	}

	if env.held(t, "uuid-"+addAddr) {
		t.Fatal("the machine is still held")
	}

	if want := []string{"uuid-" + addAddr}; len(*env.released) != 1 || (*env.released)[0] != want[0] {
		t.Fatalf("released %v, want only %v (the machines already in the cluster are not touched)", *env.released, want)
	}

	if strings.Contains(env.stderr.String(), caRotationAdvice) {
		t.Fatalf("the adoption's CA rotation advice is printed for an added machine:\n%s", env.stderr.String())
	}
}

// --plan writes nothing, not even the machine.
func TestAdoptAddMachinePlanWritesNothing(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t))

	d := env.addDeps(t)
	d.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if len(*env.writes) != 0 {
		t.Fatalf("--plan wrote: %v", *env.writes)
	}

	if _, ok := env.machine(t, "uuid-"+addAddr); ok {
		t.Fatal("--plan created the machine")
	}
}

// Drift the typed documents can hold becomes one machine-layer patch, created before the hold goes.
func TestAdoptAddMachineCapturesRepresentableDrift(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t), sysctlPatch(env.cluster.Name, map[string]string{"vm.swappiness": "10"}))

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, env.addDeps(t)); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	var p ichorv1alpha1.TalosConfigPatch

	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "adopt-uuid-" + addAddr}, &p); err != nil {
		t.Fatalf("the sysctl patch was not created: %v\n%s", err, env.stderr.String())
	}

	if p.Spec.Target == nil || p.Spec.Target.MachineRef == nil || p.Spec.Target.MachineRef.Name != "uuid-"+addAddr {
		t.Fatalf("the patch does not target the new machine: %+v", p.Spec.Target)
	}
}

// A machine that carries another cluster's CAs is refused with nothing written, and the refusal names
// the command that resets it.
func TestAdoptAddMachineRefusesAMachineOfAnotherCluster(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newFreshTestBundle(t))

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, env.addDeps(t))
	if err == nil {
		t.Fatal("runAdopt succeeded for a machine of another Talos cluster")
	}

	for _, want := range []string{addAddr, "different Talos cluster", "ichor reclaim node " + addAddr} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	if len(*env.writes) != 0 {
		t.Fatalf("a refused machine caused writes: %v", *env.writes)
	}
}

// A machine of another Talos cluster cannot even be dialled with this cluster's talosconfig: the TLS
// handshake fails with an unknown authority. That is the same refusal, naming the reset command, not a
// raw certificate error (hardware finding E3).
func TestAdoptAddMachineUnknownAuthorityNamesReclaim(t *testing.T) {
	env := newUnmanagedAddEnv(t)

	d := env.addDeps(t)
	d.ReadIdentity = func(context.Context, string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{}, errors.New(
			"rpc error: code = Unavailable desc = connection error: tls: failed to verify certificate: x509: certificate signed by unknown authority")
	}

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d)
	if err == nil {
		t.Fatal("runAdopt succeeded for a machine whose certificate is not trusted")
	}

	for _, want := range []string{addAddr, "different Talos cluster", "ichor reclaim node " + addAddr, "--cluster " + env.cluster.Name} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}

	if strings.Contains(err.Error(), "failed to verify certificate") {
		t.Errorf("the raw TLS error is shown: %v", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("a refused machine caused writes: %v", *env.writes)
	}
}

// Any other dial failure stays as it is.
func TestAdoptAddMachineOtherDialErrorsStay(t *testing.T) {
	env := newUnmanagedAddEnv(t)

	d := env.addDeps(t)
	d.ReadIdentity = func(context.Context, string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{}, errors.New("connection refused")
	}

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d)
	if err == nil || !strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "reclaim") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A node whose UUID is already a TalosMachine is not added a second time, and nothing is written.
func TestAdoptAddMachineSkipsAMachineThatExists(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t))

	d := env.addDeps(t)
	d.ReadIdentity = func(context.Context, string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: testInitMachineUUID, Serial: "s"}, nil
	}

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, d); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if len(*env.writes) != 0 {
		t.Fatalf("writes for a machine that exists: %v", *env.writes)
	}

	if !strings.Contains(env.stderr.String(), "nothing to add") {
		t.Fatalf("no explanation:\n%s", env.stderr.String())
	}
}

// A third control plane of a cluster that is configured for one is refused before anything is written.
func TestAdoptAddMachineRefusesAControlPlaneBeyondTheCount(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, roleControlPlane, newTestBundle(t))

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, env.addDeps(t))
	if err == nil || !strings.Contains(err.Error(), "spec.controlPlane.count") {
		t.Fatalf("runAdopt = %v, want the control plane count named", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("writes: %v", *env.writes)
	}
}

// A TalosCluster that does not exist cannot receive a machine: adopt the cluster first.
func TestAdoptAddMachineNeedsAnExistingCluster(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	env.newcomer(t, addAddr, testRoleWorker, newTestBundle(t))

	if err := env.k8s.Delete(context.Background(), env.cluster); err != nil {
		t.Fatal(err)
	}

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, env.addDeps(t))
	if err == nil || !strings.Contains(err.Error(), "--add-machine") {
		t.Fatalf("runAdopt = %v, want an error naming --add-machine", err)
	}
}

// A flag that means nothing for --add-machine is refused, not ignored.
func TestCheckAddMachineFlags(t *testing.T) {
	newCmd := func(args ...string) *cobra.Command {
		c := &cobra.Command{Use: "x"}
		for _, f := range addMachineExcluded {
			c.Flags().String(f, "", "")
		}

		if err := c.Flags().Parse(args); err != nil {
			t.Fatal(err)
		}

		return c
	}

	if err := checkAddMachineFlags(newCmd("--tunnel=geneve"), nil); err != nil {
		t.Fatalf("without --add-machine every flag is fine: %v", err)
	}

	if err := checkAddMachineFlags(newCmd(), []string{addAddr}); err != nil {
		t.Fatalf("--add-machine alone: %v", err)
	}

	err := checkAddMachineFlags(newCmd("--tunnel=geneve"), []string{addAddr})
	if err == nil || !strings.Contains(err.Error(), "--tunnel") {
		t.Fatalf("--tunnel with --add-machine = %v, want a refusal naming it", err)
	}
}

func TestNodeTrustsBundle(t *testing.T) {
	env := newUnmanagedAddEnv(t)
	bundle := newTestBundle(t)

	env.newcomer(t, "10.0.0.20", testRoleWorker, bundle)
	env.newcomer(t, "10.0.0.21", roleControlPlane, bundle)
	env.newcomer(t, "10.0.0.22", testRoleWorker, newFreshTestBundle(t))

	for addr, want := range map[string]bool{"10.0.0.20": true, "10.0.0.21": true, "10.0.0.22": false} {
		got, err := nodeTrustsBundle(env.live[addr], bundle)
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}

		if got != want {
			t.Errorf("%s: trusts = %v, want %v", addr, got, want)
		}
	}
}

func TestResolveClusterBundleReadsTheClustersSecret(t *testing.T) {
	cluster := newTestCluster()
	bundle := newTestBundle(t)

	secret, err := buildBundleSecret(cluster, bundle)
	if err != nil {
		t.Fatal(err)
	}

	secret.Namespace = ichorControllerNamespace

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, secret).Build()

	got, err := resolveClusterBundle(context.Background(), k8s, testNamespace, cluster.Name)
	if err != nil {
		t.Fatal(err)
	}

	want, _ := secretsbundle.BundleFingerprint(bundle)
	have, _ := secretsbundle.BundleFingerprint(got)

	if want != have {
		t.Fatal("the bundle read from the Secret is not the cluster's")
	}

	// A CA rotation under way changes what the nodes trust: refuse until it is done.
	secret.Data[secretsbundle.RotationKey] = []byte("{}")

	if err := k8s.Update(context.Background(), secret); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveClusterBundle(context.Background(), k8s, testNamespace, cluster.Name); err == nil ||
		!strings.Contains(err.Error(), "rotation") {
		t.Fatalf("resolveClusterBundle during a rotation = %v, want a refusal naming the rotation", err)
	}
}
