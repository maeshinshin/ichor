/*
Copyright 2026.

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
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
)

// The fixtures are `talosctl gen config d1 https://10.5.0.2:6443 --additional-sans 10.5.0.2,cp.example.test
// --install-disk /dev/vda --registry-mirror docker.io=https://mirror.example.test:5000` at talosctl v1.14.3
// (--talos-version v1.14.3, UnattendedInstallConfig) and v1.13.0 (legacy machine.install), generated with
// throwaway secrets. They are the shape a real cluster has: the installer image, a disk, a registry
// mirror and extra cert SANs.
const (
	classADir    = "testdata/adopt-class-a/"
	classAHost   = "docker.io"
	classADisk   = "/dev/vda"
	classAMirror = "https://mirror.example.test:5000"
	classAIP     = "10.5.0.2"
	classACPSAN  = "cp.example.test"
)

const classAMirrorDoc = `---
apiVersion: v1alpha1
kind: RegistryMirrorConfig
name: ghcr.io
endpoints:
    - url: https://ghcr-mirror.example.test/prefix
      overridePath: true
skipFallback: true
`

const classAAuthDoc = `---
apiVersion: v1alpha1
kind: RegistryAuthConfig
name: docker.io
username: someone
password: not-a-real-password
`

func classAFixture(t *testing.T, version string) (cp, wk []byte) {
	t.Helper()

	var err error

	if cp, err = os.ReadFile(classADir + version + "-controlplane.yaml"); err != nil {
		t.Fatal(err)
	}

	if wk, err = os.ReadFile(classADir + version + "-worker.yaml"); err != nil {
		t.Fatal(err)
	}

	return cp, wk
}

// classAFlow is a fresh, discovered-cluster adopt of a one control plane, one worker cluster whose
// nodes hold cp and wk, against an API server that accepts the Ichor kinds.
func classAFlow(t *testing.T, talosVersion string, cp, wk []byte) (*flowEnv, adoptDeps) {
	t.Helper()

	env := newFlowEnv(t, cniCilium, nil)
	env.live = map[string][]byte{flowCPAddr: cp, flowWKAddr: wk}
	env.k8s = fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	provider, err := configloader.NewFromBytes(cp)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := secrets.NewBundleFromConfig(secrets.NewFixedClock(time.Now()), provider)
	if err != nil {
		t.Fatal(err)
	}

	members := []ClusterMember{
		{Hostname: "cp", Addresses: []string{flowCPAddr}, Role: roleControlPlane},
		{Hostname: "wk", Addresses: []string{flowWKAddr}, Role: testRoleWorker},
	}

	deps := env.deps(t)
	deps.Bundle = bundle
	deps.CNIUnmanaged = true
	deps.Seed = flowCPAddr
	deps.ReleaseWait = 50 * time.Millisecond
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &membersReader{current: env.live[endpoint], version: talosVersion, members: members}, nil
	}
	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "uuid-" + address, Serial: "s"}, nil
	}

	return env, deps
}

// planOutput runs the plan and returns what adopt said; a plan blocked by class C is an error
// with the report on stderr, which blocked tells the caller to expect.
func planOutput(t *testing.T, env *flowEnv, deps adoptDeps, blocked bool) string {
	t.Helper()

	deps.PlanOnly = true

	err := runAdopt(context.Background(), "d1", testNamespace, deps)
	if (err != nil) != blocked {
		t.Fatalf("plan: %v (blocked expected: %v)\n%s", err, blocked, env.stderr.String())
	}

	return env.stderr.String()
}

func TestAdoptMapsClassAValuesOfAForeignCluster(t *testing.T) {
	const (
		image  = "factory.talos.dev/metal-installer/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba:v1.14.3"
		cpUUID = "uuid-" + flowCPAddr
		wkUUID = "uuid-" + flowWKAddr
	)

	cases := []struct {
		name, fixture, talos string
		extra                string
		wipe                 bool
		registries           []ichorv1alpha1.TalosClusterRegistry
	}{
		{"v1.14 documents", "v114", ver1143, "", false, []ichorv1alpha1.TalosClusterRegistry{
			{Host: classAHost, Endpoints: []string{classAMirror}},
		}},
		{"v1.13 legacy machine.install", "v113", "v1.13.0", "", false, []ichorv1alpha1.TalosClusterRegistry{
			{Host: classAHost, Endpoints: []string{classAMirror}},
		}},
		{"v1.14 with overridePath, skipFallback and wipe", "v114", ver1143, classAMirrorDoc, true, []ichorv1alpha1.TalosClusterRegistry{
			{Host: classAHost, Endpoints: []string{classAMirror}},
			{Host: "ghcr.io", Endpoints: []string{"https://ghcr-mirror.example.test/prefix"}, OverridePath: true, SkipFallback: true},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp, wk := classAFixture(t, tc.fixture)

			if tc.wipe {
				cp = []byte(strings.ReplaceAll(string(cp), "wipe: false", "wipe: true"))
				wk = []byte(strings.ReplaceAll(string(wk), "wipe: false", "wipe: true"))
			}

			cp = append(cp, tc.extra...)
			wk = append(wk, tc.extra...)

			env, deps := classAFlow(t, tc.talos, cp, wk)

			out := planOutput(t, env, deps, false)
			if strings.Contains(out, "cannot patch automatically") {
				t.Fatalf("class A values were reported as unrepresentable:\n%s", out)
			}

			// A real run writes the objects, then waits for a controller this test does not have.
			deps.PlanOnly, deps.AssumeYes = false, true
			if err := runAdopt(context.Background(), "d1", testNamespace, deps); err == nil || !strings.Contains(err.Error(), "did not become Ready") {
				t.Fatalf("runAdopt = %v, want the Ready timeout\n%s", err, env.stderr.String())
			}

			var cluster ichorv1alpha1.TalosCluster
			if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "d1"}, &cluster); err != nil {
				t.Fatal(err)
			}

			if want := []string{classAIP, classACPSAN}; !slices.Equal(cluster.Spec.CertSANs, want) {
				t.Errorf("spec.certSANs = %v, want %v", cluster.Spec.CertSANs, want)
			}

			if !jsonEqual(cluster.Spec.Registries, tc.registries) {
				t.Errorf("spec.registries = %+v, want %+v", cluster.Spec.Registries, tc.registries)
			}

			for _, name := range []string{cpUUID, wkUUID} {
				var m ichorv1alpha1.TalosMachine
				if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, &m); err != nil {
					t.Fatal(err)
				}

				want := &ichorv1alpha1.TalosMachineInstall{Disk: ichorv1alpha1.TalosMachineDisk{DevPath: classADisk}, Image: image, Wipe: tc.wipe}
				if !jsonEqual(m.Spec.Install, want) {
					t.Errorf("%s: spec.install = %+v, want %+v", name, m.Spec.Install, want)
				}
			}

			// The rerun sees the written objects: nothing differs, nothing is left to patch.
			deps.PlanOnly, deps.AssumeYes = true, false
			env.stderr.Reset()

			again := planOutput(t, env, deps, false)
			for _, bad := range []string{"cannot patch automatically", "drift found", "Ichor-owned"} {
				if strings.Contains(again, bad) {
					t.Errorf("the rerun plan still reports drift (%q):\n%s", bad, again)
				}
			}

			if n := strings.Count(again, "already matches a fresh render"); n != 2 {
				t.Errorf("the rerun plan matched %d machines, want 2:\n%s", n, again)
			}
		})
	}
}

// What has no representation stays class C with a hint: registry credentials never go into spec.
func TestAdoptKeepsUnrepresentableRegistryDriftClassC(t *testing.T) {
	cp, wk := classAFixture(t, "v114")

	env, deps := classAFlow(t, ver1143, append(cp, classAAuthDoc...), append(slices.Clone(wk), classAAuthDoc...))

	out := planOutput(t, env, deps, true)
	if !strings.Contains(out, "RegistryAuthConfig") || !strings.Contains(out, "authSecretRef") {
		t.Fatalf("registry credentials were not kept as class C with the hint:\n%s", out)
	}

	if strings.Contains(out, "not-a-real-password") {
		t.Fatalf("a credential reached the output:\n%s", out)
	}
}

// Nodes whose certSANs differ are not blocked: spec.certSANs is the union, and the plan lists what
// each node gains for consent (ADR 0141).
func TestAdoptTakesTheUnionOfDifferingCertSANs(t *testing.T) {
	cp, wk := classAFixture(t, "v114")
	wk = []byte(strings.ReplaceAll(string(wk), "- cp.example.test", "- other.example.test"))

	env, deps := classAFlow(t, ver1143, cp, wk)

	out := planOutput(t, env, deps, false)
	for _, want := range []string{
		"Ichor-owned settings its first render will change (need consent): machine.certSANs",
		"spec.certSANs, rendered on every node, is the union of the nodes' lists: 10.5.0.2, cp.example.test, other.example.test",
		"machine \"uuid-" + flowCPAddr + "\": its certificate gains the subject alternative names other.example.test",
		"machine \"uuid-" + flowWKAddr + "\": its certificate gains the subject alternative names cp.example.test",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "cannot patch automatically") {
		t.Fatalf("differing SANs still block:\n%s", out)
	}

	deps.PlanOnly, deps.AssumeYes = false, true
	_ = runAdopt(context.Background(), "d1", testNamespace, deps)

	var cluster ichorv1alpha1.TalosCluster
	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "d1"}, &cluster); err != nil {
		t.Fatal(err)
	}

	if want := []string{classAIP, classACPSAN, "other.example.test"}; !slices.Equal(cluster.Spec.CertSANs, want) {
		t.Errorf("spec.certSANs = %v, want %v", cluster.Spec.CertSANs, want)
	}
}

// A node that states a SAN the cluster's list lacks would lose it, so it stays blocked (the add-machine flow's
// TestAdoptAddMachineBlockedMachineIsNotNothingToAdopt covers the plan end to end).
func TestAdoptBlocksANodeThatWouldLoseACertSAN(t *testing.T) {
	st := &adoptState{cluster: &ichorv1alpha1.TalosCluster{Spec: ichorv1alpha1.TalosClusterSpec{CertSANs: []string{classAIP}}}}
	n := &adoptNode{M: &ichorv1alpha1.TalosMachine{}, Facts: &TalosFacts{CertSANs: []string{classAIP, classACPSAN}}}

	if _, covered := st.sansGained(n); covered {
		t.Fatal("a node with a SAN outside spec.certSANs was treated as covered")
	}

	n.Facts.CertSANs = []string{classAIP}

	if gained, covered := st.sansGained(n); !covered || len(gained) != 0 {
		t.Fatalf("a node stating a subset: gained %v, covered %v", gained, covered)
	}
}

func TestDiskFromExpression(t *testing.T) {
	for in, want := range map[string]string{
		`disk.dev_path == "/dev/vda"`:                         "/dev/vda",
		`disk.wwid == "naa.1"`:                                "wwid:naa.1",
		`disk.serial == "S1"`:                                 "serial:S1",
		`disk.bus_path == "/pci0000:00/0000:00:1f.2"`:         "/pci0000:00/0000:00:1f.2",
		`"/dev/disk/by-id/ata-X" in disk.symlinks`:            "/dev/disk/by-id/ata-X",
		`"/dev/disk/by-path/pci-0000" in disk.symlinks`:       "",
		`disk.size > 100u * GB`:                               "",
		`disk.dev_path == "/dev/vda" && disk.rotational`:      "",
		`disk.dev_path == "sda"`:                              "",
		`disk.bus_path == "/dev/vda"`:                         "",
		`disk.wwid == ""`:                                     "",
		`disk.dev_path == "/dev/disk/by-id/ata-X"`:            "",
		`"/dev/disk/by-id/a" in disk.symlinks || disk.x == 1`: "",
	} {
		if got := diskFromExpression(in); got != want {
			t.Errorf("diskFromExpression(%q) = %q, want %q", in, got, want)
		}
	}
}
