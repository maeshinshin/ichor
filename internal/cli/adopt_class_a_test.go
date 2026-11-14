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
	"fmt"
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

	return classAFlowOf(t, talosVersion, map[string][]byte{flowCPAddr: cp, flowWKAddr: wk}, []ClusterMember{
		{Hostname: "cp", Addresses: []string{flowCPAddr}, Role: roleControlPlane},
		{Hostname: "wk", Addresses: []string{flowWKAddr}, Role: testRoleWorker},
	})
}

// classAFlowOf is classAFlow for any set of nodes: live maps each address to its configuration.
func classAFlowOf(t *testing.T, talosVersion string, live map[string][]byte, members []ClusterMember) (*flowEnv, adoptDeps) {
	t.Helper()

	env := newFlowEnv(t, cniCilium, nil)
	env.live = live
	env.k8s = fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	provider, err := configloader.NewFromBytes(live[members[0].Addresses[0]])
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := secrets.NewBundleFromConfig(secrets.NewFixedClock(time.Now()), provider)
	if err != nil {
		t.Fatal(err)
	}

	deps := env.deps(t)
	deps.Bundle = bundle
	deps.CNIUnmanaged = true
	deps.Seed = members[0].Addresses[0]
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

			var patches ichorv1alpha1.TalosConfigPatchList
			if err := env.k8s.List(context.Background(), &patches); err != nil {
				t.Fatal(err)
			}

			for _, p := range patches.Items {
				if slices.ContainsFunc(p.Spec.Documents, func(d ichorv1alpha1.TalosConfigPatchDocument) bool {
					return d.Config != nil && d.Config.MachineConfig != nil && len(d.Config.MachineConfig.MachineCertSANs) > 0
				}) {
					t.Errorf("patch %s captures certSANs although every node agrees", p.Name)
				}
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

// Control planes whose machine.certSANs differ are not blocked and need no consent: each node's own
// list beyond the cluster's is captured into its patch, so nothing on a node changes (ADR 0143).
func TestAdoptCapturesEachNodesDifferingCertSANs(t *testing.T) {
	cp, _ := classAFixture(t, "v114")
	addrs := []string{flowCPAddr, addAddr, "10.0.0.13"}
	live, members := map[string][]byte{}, make([]ClusterMember, 0, len(addrs))

	for i, addr := range addrs {
		// Only machine.certSANs differs; the kube-apiserver's SANs are the same on every control plane.
		own := strings.Replace(string(cp), "    certSANs:\n        - 10.5.0.2\n        - cp.example.test\n",
			fmt.Sprintf("    certSANs:\n        - 10.5.0.2\n        - cp.example.test\n        - cp%d.example.test\n", i), 1)
		live[addr] = []byte(own)
		members = append(members, ClusterMember{Hostname: fmt.Sprintf("cp%d", i), Addresses: []string{addr}, Role: roleControlPlane})
	}

	env, deps := classAFlowOf(t, ver1143, live, members)

	out := planOutput(t, env, deps, false)
	for _, bad := range []string{"cannot patch automatically", "need consent", "gains the subject alternative names"} {
		if strings.Contains(out, bad) {
			t.Fatalf("the plan reports %q:\n%s", bad, out)
		}
	}

	deps.PlanOnly, deps.AssumeYes = false, true
	_ = runAdopt(context.Background(), "d1", testNamespace, deps)

	var cluster ichorv1alpha1.TalosCluster
	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "d1"}, &cluster); err != nil {
		t.Fatal(err)
	}

	if want := []string{classAIP, classACPSAN}; !slices.Equal(cluster.Spec.CertSANs, want) {
		t.Errorf("spec.certSANs = %v, want the control planes' shared kube-apiserver list %v", cluster.Spec.CertSANs, want)
	}

	for i, addr := range addrs {
		var p ichorv1alpha1.TalosConfigPatch
		if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "adopt-uuid-" + addr}, &p); err != nil {
			t.Fatal(err)
		}

		var got []string
		for _, d := range p.Spec.Documents {
			if d.Config != nil && d.Config.MachineConfig != nil {
				got = append(got, d.Config.MachineConfig.MachineCertSANs...)
			}
		}

		if want := []string{fmt.Sprintf("cp%d.example.test", i)}; !slices.Equal(got, want) {
			t.Errorf("%s: the patch's machine.certSANs = %v, want %v (what spec.certSANs does not render)", addr, got, want)
		}
	}

	deps.PlanOnly, deps.AssumeYes = true, false
	env.stderr.Reset()

	if again := planOutput(t, env, deps, false); strings.Count(again, "already matches a fresh render") != 3 {
		t.Errorf("the rerun plan does not match all three machines:\n%s", again)
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

// userCluster rewrites a v1.14 control plane into the shape a real cluster showed: machine.certSANs
// holds only localhost, its kube-apiserver SANs (KubeAPIServerConfig.certExtraSANs) are absent, and one
// interface carries an address, a default route and a control-plane virtual IP.
func userCluster(t *testing.T, cp []byte) []byte {
	t.Helper()

	out := string(cp)
	for _, r := range [][2]string{
		{"    certSANs:\n        - 10.5.0.2\n        - cp.example.test\n", "    certSANs:\n        - localhost\n" +
			"    network:\n        interfaces:\n            - interface: enp1s0\n              addresses:\n                  - 10.0.1.5/16\n" +
			"              routes:\n                  - network: 0.0.0.0/0\n                    gateway: 10.0.0.1\n" +
			"              vip:\n                  ip: 10.0.1.100\n"},
		{"certExtraSANs:\n    - 10.5.0.2\n    - cp.example.test\n", ""},
	} {
		if !strings.Contains(out, r[0]) {
			t.Fatalf("fixture lacks %q", r[0])
		}

		out = strings.Replace(out, r[0], r[1], 1)
	}

	return []byte(out)
}

func userClusterFlow(t *testing.T, talosVersion string, mutate func(i int, cfg string) string) (*flowEnv, adoptDeps, []string) {
	t.Helper()

	cp, _ := classAFixture(t, "v114")
	addrs := []string{flowCPAddr, addAddr, "10.0.0.13"}
	live, members := map[string][]byte{}, make([]ClusterMember, 0, len(addrs))

	for i, addr := range addrs {
		cfg := string(userCluster(t, cp))
		if mutate != nil {
			cfg = mutate(i, cfg)
		}

		live[addr] = []byte(cfg)
		members = append(members, ClusterMember{Hostname: fmt.Sprintf("cp%d", i), Addresses: []string{addr}, Role: roleControlPlane})
	}

	env, deps := classAFlowOf(t, talosVersion, live, members)

	return env, deps, addrs
}

// Nodes whose machine.certSANs hold a SAN that their kube-apiserver SANs lack must not put it into
// spec.certSANs, which renders into both: every node keeps it in its own patch, with its interfaces and
// the virtual IP, and nothing is left to differ (ADR 0143).
func TestAdoptKeepsSpecCertSANsToWhatEveryNodeSharesInBothPlaces(t *testing.T) {
	env, deps, addrs := userClusterFlow(t, "v1.14.3", nil)

	if out := planOutput(t, env, deps, false); strings.Contains(out, "cannot patch automatically") {
		t.Fatalf("the plan is blocked:\n%s", out)
	}

	deps.PlanOnly, deps.AssumeYes = false, true
	_ = runAdopt(context.Background(), "d1", testNamespace, deps)

	var cluster ichorv1alpha1.TalosCluster
	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "d1"}, &cluster); err != nil {
		t.Fatal(err)
	}

	if len(cluster.Spec.CertSANs) != 0 {
		t.Errorf("spec.certSANs = %v, want empty", cluster.Spec.CertSANs)
	}

	for _, addr := range addrs {
		var p ichorv1alpha1.TalosConfigPatch
		if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "adopt-uuid-" + addr}, &p); err != nil {
			t.Fatal(err)
		}

		var sans []string

		var ifaces []*ichorv1alpha1.TalosConfigPatchDocument

		for _, d := range p.Spec.Documents {
			if d.Config == nil || d.Config.MachineConfig == nil {
				continue
			}

			sans = append(sans, d.Config.MachineConfig.MachineCertSANs...)

			if n := d.Config.MachineConfig.MachineNetwork; n != nil && len(n.NetworkInterfaces) == 1 {
				i := n.NetworkInterfaces[0]
				if i.DeviceInterface != "enp1s0" || len(i.DeviceRoutes) != 1 || i.DeviceVIPConfig == nil || i.DeviceVIPConfig.SharedIP != "10.0.1.100" {
					t.Errorf("%s: the captured interface lost its routes or vip: %+v", addr, i)
				}

				ifaces = append(ifaces, &d)
			}
		}

		if !slices.Equal(sans, []string{"localhost"}) || len(ifaces) != 1 {
			t.Errorf("%s: patch machine.certSANs = %v, interfaces documents = %d; want [localhost] and 1", addr, sans, len(ifaces))
		}
	}

	deps.PlanOnly, deps.AssumeYes = true, false
	env.stderr.Reset()

	again := planOutput(t, env, deps, false)
	if strings.Contains(again, "drift found") || strings.Count(again, "already matches a fresh render") != 3 {
		t.Errorf("the rerun plan reports drift:\n%s", again)
	}
}

// On a v1.14 contract the kube-apiserver SANs live in KubeAPIServerConfig, which a patch cannot carry:
// a node with one beyond what every node shares blocks, and only that path is named.
func TestAdoptBlocksOnlyTheAPIServerSANsThatCannotBeCaptured(t *testing.T) {
	cp, _ := classAFixture(t, "v114")
	addrs := []string{flowCPAddr, addAddr}
	live, members := map[string][]byte{}, make([]ClusterMember, 0, len(addrs))

	for i, addr := range addrs {
		cfg := string(cp)
		if i == 0 {
			cfg = strings.Replace(cfg, "certExtraSANs:\n    - 10.5.0.2\n    - cp.example.test\n", "certExtraSANs:\n    - 10.5.0.2\n    - cp.example.test\n    - extra.example.test\n", 1)
		}

		live[addr] = []byte(cfg)
		members = append(members, ClusterMember{Hostname: fmt.Sprintf("cp%d", i), Addresses: []string{addr}, Role: roleControlPlane})
	}

	env, deps := classAFlowOf(t, ver1143, live, members)

	out := planOutput(t, env, deps, false)
	if !strings.Contains(out, "blocked") || !strings.Contains(out, "KubeAPIServerConfig.certExtraSANs") || strings.Contains(out, "machine.network.interfaces") {
		t.Errorf("the plan does not name only the kube-apiserver SANs:\n%s", out)
	}
}
