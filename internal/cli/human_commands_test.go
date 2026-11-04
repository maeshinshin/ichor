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
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// These tests pin the human output of the converted commands (docs/adr/0118) on a pipe, so with no
// colour codes, and that -o yaml and -o json still print what they printed before.

func setHumanTestFlags(t *testing.T, format string) {
	t.Helper()
	setAPITestFlags(t, true)

	outputFormat = format

	fixedHumanClock(t)
}

func ago(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(humanTestNow.Add(-d)) }

func cond(typ, status, reason, msg string, age time.Duration) *pb.Condition {
	return &pb.Condition{Type: typ, Status: status, Reason: reason, Message: msg, LastTransitionTime: ago(age)}
}

func initshowCluster() *pb.Cluster {
	return &pb.Cluster{
		Ref: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: humanCluster}, TalosVersion: testTalosVersion, KubernetesVersion: humanK8s,
		Endpoint: "10.244.1.136", EndpointType: endpointNode, ControlPlaneCount: 1, ControlPlaneSchedulable: true,
		CniPreset: string(detectedCilium), CniTunnel: "geneve", KubeProxyMode: "enabled", CreatedAt: ago(5 * time.Minute),
		PodSubnets: []string{testPodCIDR}, ServiceSubnets: []string{humanSvcCIDR},
		Versions: []*pb.ComponentVersion{
			{Name: compTalos, Current: testTalosVersion, Target: testTalosVersion, State: humanUpToDate},
			{Name: "version-contract", Current: testVersionContract, Target: testVersionContract, State: humanUpToDate},
			{Name: compKubernetes, Current: "v1.36.4", Target: humanK8s, State: "behind"},
			{Name: string(detectedCilium), Current: testCiliumVersion, Target: testCiliumVersion, State: humanUpToDate},
		},
		Certificates: []*pb.CertificateExpiry{
			{Name: compTalos, NotAfter: timestamppb.New(humanTestNow.Add(364 * 24 * time.Hour))},
			{Name: humanEtcd, NotAfter: timestamppb.New(humanTestNow.Add(-2 * 24 * time.Hour))},
		},
		Conditions: []*pb.Condition{
			cond(conditionReady, condTrue, "Converged", "all machines match their rendered configuration", 4*time.Minute),
			cond("CertificatesExpiring", condFalse, "NoneExpiring", "no CA expires within 30 days", 4*time.Minute),
			cond("NetworkPoliciesApplied", condFalse, "CNINotCilium", "the detected CNI is not Cilium", time.Minute),
		},
	}
}

func TestClusterStatusHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderClusterStatus(newHuman(&out), initshowCluster()); err != nil {
		t.Fatal(err)
	}

	want := "initshow · ichor-system\n" +
		"  Talos        v1.14.5\n" +
		"  Kubernetes   v1.36.6\n" +
		"  Cilium       1.20.4\n" +
		"  Endpoint     10.244.1.136 (node)\n" +
		"  Health       ✓ ready\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestClusterStatusHumanOutputForAnUnmanagedAndAnUnreadyCluster(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	cl := initshowCluster()
	cl.CniPreset, cl.CniDetected = ichorv1alpha1.TalosClusterCNIPresetUnmanaged, string(detectedFlannel)
	cl.Conditions = []*pb.Condition{cond(conditionReady, condFalse, "MachinesNotReady", "1 of 3 machines", time.Minute)}

	var out bytes.Buffer
	if err := renderClusterStatus(newHuman(&out), cl); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"  CNI          unmanaged (flannel), not managed by Ichor\n", "  Health       ✗ not ready (MachinesNotReady)\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestClusterGetHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderClusterGet(newHuman(&out), initshowCluster()); err != nil {
		t.Fatal(err)
	}

	want := `initshow · ichor-system
  Health       ✓ ready
  Created      5m ago

Versions
  Talos        v1.14.5
  Contract     v1.14
  Kubernetes   v1.36.4 (behind: this Ichor ships v1.36.6)
  Cilium       1.20.4

Endpoint
  Address      10.244.1.136
  Type         node

Network
  Pods         10.244.0.0/16
  Services     10.96.0.0/12

CNI
  Preset       cilium
  Tunnel       geneve
  Kube-proxy   enabled

Control plane
  Nodes        1
  Schedulable  yes

Certificates
  talos        2027-11-03 (in 364d)
  etcd         2026-11-02 (expired 2d ago)

Conditions
  ✓ Ready                   Converged     all machines match their rendered configuration
  ✓ CertificatesExpiring    NoneExpiring  no CA expires within 30 days
  ! NetworkPoliciesApplied  CNINotCilium  the detected CNI is not Cilium
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestClusterListHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	other := initshowCluster()
	other.Ref = &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: "second"}
	other.CniPreset, other.CniDetected = ichorv1alpha1.TalosClusterCNIPresetUnmanaged, string(detectedFlannel)
	other.Conditions, other.CreatedAt = []*pb.Condition{cond("Transitioning", condTrue, "Rolling", "", time.Minute)}, ago(3*24*time.Hour)

	var out bytes.Buffer
	if err := renderClusterList(newHuman(&out), &pb.ListClustersResponse{Clusters: []*pb.Cluster{initshowCluster(), other}}); err != nil {
		t.Fatal(err)
	}

	want := "NAME      TALOS    KUBERNETES  CNI                  ENDPOINT      READY  AGE\n" +
		"initshow  v1.14.5  v1.36.6     cilium               10.244.1.136  ✓      5m\n" +
		"second    v1.14.5  v1.36.6     unmanaged (flannel)  10.244.1.136  …      3d\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()

	if err := renderClusterList(newHuman(&out), &pb.ListClustersResponse{}); err != nil || out.String() != "no clusters\n" {
		t.Errorf("empty list: %q, %v", out.String(), err)
	}
}

const (
	testPodCIDR       = "10.244.0.0/16"
	testCiliumVersion = "1.20.4"
	cpUUID            = "fa70962a-4fd9-4d01-88c5-f9966adcda99"
	pendingUID        = "0b1c2d3e-4f50-6071-8293-a4b5c6d7e8f9"
)

func machinesFixture() []*pb.Machine {
	return []*pb.Machine{
		{
			Ref: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: cpUUID}, Cluster: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: humanCluster},
			Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, ApprovalState: pb.MachineApprovalState_MACHINE_APPROVAL_STATE_APPROVED,
			Endpoint: "10.244.1.136:50000", NodeName: "ichor-e2e-initshow-cp", CreatedAt: ago(5 * time.Minute),
			Identity:         &pb.MachineIdentity{Uuid: cpUUID, Serial: "c427f61b", MacAddresses: []string{"a6:c8:83:c3:79:d9"}},
			Install:          &pb.MachineInstall{Disk: &pb.MachineDisk{DevPath: classADisk}},
			ApprovalDecision: &pb.MachineApprovalDecision{Actor: actorCLI, At: ago(4 * time.Minute), Reason: "first node"},
			Conditions: []*pb.Condition{
				cond(conditionReady, condTrue, "Converged", "node configuration matches the rendered configuration", 4*time.Minute),
				cond("ConfigWarnings", condFalse, "DryRunWarnings", "dry run reported no warnings", 4*time.Minute),
			},
		},
		{
			Ref: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: pendingUID}, Cluster: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: humanCluster},
			ApprovalState: pb.MachineApprovalState_MACHINE_APPROVAL_STATE_PENDING, Endpoint: "10.244.1.150:50000", CreatedAt: ago(30 * time.Second),
			DiscoveredDisks: []*pb.DiscoveredDisk{{DevPath: classADisk, SizeBytes: 40 << 30, Model: "QEMU HARDDISK", Transport: "virtio"}},
			DiscoveredLinks: []*pb.DiscoveredLink{{Name: "eth0", PermanentMac: "a6:c8:83:00:00:01", LinkUp: true, SpeedMbit: 10000, Driver: "virtio_net"}},
		},
	}
}

func TestMachineListHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderMachineList(newHuman(&out), &pb.ListMachinesResponse{Machines: machinesFixture()}); err != nil {
		t.Fatal(err)
	}

	want := "NAME      ROLE           APPROVAL  READY  ENDPOINT            NODE                   AGE\n" +
		"fa70962a  control-plane  approved  ✓      10.244.1.136:50000  ichor-e2e-initshow-cp  5m\n" +
		"0b1c2d3e  -              pending   -      10.244.1.150:50000  -                      30s\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestMachineListKeepsFullNamesWhenShortOnesClash(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	ms := machinesFixture()
	ms[1].Ref.Name = "fa70962a-ffff-ffff-ffff-ffffffffffff"
	ms[1].Cluster.Name = humanElsewhere

	var out bytes.Buffer
	if err := renderMachineList(newHuman(&out), &pb.ListMachinesResponse{Machines: ms}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{cpUUID + "  initshow   control-plane", "fa70962a-ffff-ffff-ffff-ffffffffffff  elsewhere"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	if !strings.HasPrefix(out.String(), "NAME") || !strings.Contains(strings.SplitN(out.String(), "\n", 2)[0], "CLUSTER") {
		t.Errorf("machines of two clusters need a CLUSTER column:\n%s", out.String())
	}
}

func TestMachineGetHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderMachineGet(newHuman(&out), machinesFixture()[0]); err != nil {
		t.Fatal(err)
	}

	want := `fa70962a-4fd9-4d01-88c5-f9966adcda99 · ichor-system
  Cluster      initshow
  Role         control-plane
  Approval     approved by ichor-cli (4m ago): first node
  Health       ✓ ready
  Node         ichor-e2e-initshow-cp
  Endpoint     10.244.1.136:50000
  Created      5m ago

Identity
  UUID         fa70962a-4fd9-4d01-88c5-f9966adcda99
  Serial       c427f61b
  MACs         a6:c8:83:c3:79:d9

Install
  Disk         /dev/vda

Conditions
  ✓ Ready           Converged       node configuration matches the rendered configuration
  ✓ ConfigWarnings  DryRunWarnings  dry run reported no warnings
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestMachineGetHumanOutputForAPendingMachineShowsWhatDiscoveryFound(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := renderMachineGet(newHuman(&out), machinesFixture()[1]); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"  Approval     pending\n", "Discovered disks\n  /dev/vda     40 GiB · QEMU HARDDISK · virtio\n",
		"Discovered NICs\n  eth0         a6:c8:83:00:00:01 · up · 10000 Mbit/s · virtio_net\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "Health") {
		t.Errorf("a pending machine has no health yet:\n%s", out.String())
	}
}

func TestVersionHumanOutput(t *testing.T) {
	info := versionInfo{
		Client:  versionClient{Version: testSettingsVersion},
		Release: &versionRelease{Talos: testTalosVersion, VersionContract: testVersionContract, Kubernetes: humanK8s, Cilium: "1.20.4"},
		Server:  &versionServer{InstalledVersion: testSettingsVersion},
	}

	var out bytes.Buffer
	if err := writeVersion(&out, outputHuman, info); err != nil {
		t.Fatal(err)
	}

	want := "ichor        v0.1.0\n" +
		"installed    v0.1.0\n" +
		"new installs Talos v1.14.5 · Kubernetes v1.36.6 · Cilium 1.20.4 (contract v1.14)\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	info.Server.SpecVersion, info.Server.LatestRelease = "v0.2.0", humanLatest
	out.Reset()

	if err := writeVersion(&out, outputHuman, info); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"upgrading    v0.2.0\n", "latest       v0.3.0 available\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	out.Reset()

	info.Server = nil
	if err := writeVersion(&out, outputHuman, info); err != nil || strings.Contains(out.String(), "installed") {
		t.Errorf("--client prints no installed line: %q, %v", out.String(), err)
	}
}

func TestUIHumanOutput(t *testing.T) {
	pod := uiTestPod(uiTestPodA)
	k8sClient := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiTestService(), pod).Build()
	stderr, _ := runUITest(t, k8sClient, newFakeForwarder(nil))

	want := "✓ ichor-ui  http://localhost:8080/\n" +
		"  port-forward to " + uiTestNamespace + "/pod-a · Ctrl-C to stop\n" +
		"! the port-forward to pod/pod-a ended; looking for another pod\n" +
		"✓ re-established the port-forward -> pod/pod-a\n"
	if stderr != want {
		t.Errorf("got:\n%s\nwant:\n%s", stderr, want)
	}
}

// The machine formats print what they printed before human output became the default.

func TestMachineFormatsOfTheConvertedCommandsAreUnchanged(t *testing.T) {
	setHumanTestFlags(t, outputYAML)

	cl := &pb.Cluster{Ref: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: humanCluster}, TalosVersion: testTalosVersion, Endpoint: "10.244.1.136"}
	m := &pb.Machine{
		Ref: &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: "m"}, Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE,
		ApprovalState: pb.MachineApprovalState_MACHINE_APPROVAL_STATE_APPROVED,
	}

	for name, tc := range map[string]struct {
		print func(*bytes.Buffer) error
		want  string
	}{
		"cluster list yaml": {func(b *bytes.Buffer) error {
			return printAPI(b, &pb.ListClustersResponse{Clusters: []*pb.Cluster{cl}}, renderClusterList)
		}, "clusters:\n- endpoint: 10.244.1.136\n  ref:\n    name: initshow\n    namespace: ichor-system\n  talosVersion: v1.14.5\n"},
		"machine get yaml": {func(b *bytes.Buffer) error {
			return printAPI(b, &pb.GetMachineResponse{Machine: m}, func(h *human, r *pb.GetMachineResponse) error { return renderMachineGet(h, r.GetMachine()) })
		}, "machine:\n  approvalState: MACHINE_APPROVAL_STATE_APPROVED\n  ref:\n    name: m\n    namespace: ichor-system\n  role: MACHINE_ROLE_CONTROL_PLANE\n"},
		"version yaml": {func(b *bytes.Buffer) error {
			return writeVersion(b, outputYAML, versionInfo{Client: versionClient{Version: testSettingsVersion}, Server: &versionServer{InstalledVersion: testSettingsVersion}})
		}, "client:\n  version: v0.1.0\nserver:\n  installedVersion: v0.1.0\n"},
		"version json": {func(b *bytes.Buffer) error {
			return writeVersion(b, outputJSON, versionInfo{Client: versionClient{Version: testSettingsVersion}})
		}, "{\n  \"client\": {\n    \"version\": \"v0.1.0\"\n  }\n}\n"},
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tc.print(&out); err != nil {
				t.Fatal(err)
			}

			if out.String() != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", out.String(), tc.want)
			}
		})
	}
}

func TestMachineFormatOfClusterStatusIsTheKeyValueLines(t *testing.T) {
	setHumanTestFlags(t, outputYAML)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(statusFixtureCluster()).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	for _, format := range []string{outputYAML, outputJSON} {
		outputFormat = format

		var out bytes.Buffer
		if err := runClusterStatus(context.Background(), c, &out, "adopt-cluster"); err != nil {
			t.Fatal(err)
		}

		want := "cluster: default/adopt-cluster\ntalos: v1.14.5\nkubernetes: " + statusFixtureCluster().Spec.KubernetesVersion +
			"\nendpoint: 10.0.0.5\ncni: cilium\n"
		if out.String() != want {
			t.Errorf("-o %s: got %q, want %q", format, out.String(), want)
		}
	}
}

func statusFixtureCluster() *ichorv1alpha1.TalosCluster {
	cl := newTestCluster()
	cl.CreationTimestamp = metav1.NewTime(humanTestNow.Add(-5 * time.Minute))
	cl.Status.Conditions = []metav1.Condition{{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Converged", Message: "ok"}}

	return cl
}

// An unconverted command prints YAML when -o is unset, so nothing prints nothing.
func TestAnUnconvertedCommandPrintsYAMLWhenOutputIsUnset(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer
	if err := writeMessage(&out, outputFormat, &pb.ObjectRef{Namespace: "ns", Name: "name-1"}); err != nil {
		t.Fatal(err)
	}

	if want := "name: name-1\nnamespace: ns\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}

	resp := connect.NewResponse(&pb.ListClustersResponse{Clusters: []*pb.Cluster{{Ref: &pb.ObjectRef{Name: "x"}}}})

	out.Reset()

	if err := printAPI(&out, resp.Msg, nil); err != nil || !strings.HasPrefix(out.String(), "clusters:\n") {
		t.Errorf("no human view: %q, %v", out.String(), err)
	}
}

// The whole path, from a TalosCluster and a TalosMachine through the in-process API to the table.
func TestHumanOutputThroughTheInProcessAPI(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	cl := statusFixtureCluster()
	m := newTestMachine(cl.Name)
	m.Name, m.CreationTimestamp = cpUUID, metav1.NewTime(humanTestNow.Add(-2*time.Hour))
	m.Status.Conditions = []metav1.Condition{{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Converged"}}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cl, m).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out bytes.Buffer

	resp, err := c.Machine.ListMachines(context.Background(), connect.NewRequest(&pb.ListMachinesRequest{}))
	if err != nil {
		t.Fatal(err)
	}

	if err := printAPI(&out, resp.Msg, renderMachineList); err != nil {
		t.Fatal(err)
	}

	want := "NAME      ROLE    APPROVAL  READY  ENDPOINT         NODE  AGE\n" +
		"fa70962a  worker  approved  ✓      10.0.0.10:50000  -     2h\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()

	if err := runClusterStatus(context.Background(), c, &out, cl.Name); err != nil {
		t.Fatal(err)
	}

	if want := "adopt-cluster · default\n"; !strings.HasPrefix(out.String(), want) || !strings.Contains(out.String(), "Endpoint     10.0.0.5 (vip)\n") ||
		!strings.Contains(out.String(), "Health       ✓ ready\n") {
		t.Errorf("status:\n%s", out.String())
	}
}

func TestVersionRowPrintsTheBareVersionUnlessItDiffers(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    *pb.ComponentVersion
		want string
	}{
		{"equal, empty state", &pb.ComponentVersion{Current: "v1.14.5", Target: "v1.14.5"}, "v1.14.5"},
		{"equal, up to date", &pb.ComponentVersion{Current: "v1.14.5", Target: "v1.14.5", State: stateUpToDate}, "v1.14.5"},
		{"equal, unknown", &pb.ComponentVersion{Current: "v1.14.5", Target: "v1.14.5", State: "unknown"}, "v1.14.5"},
		{"no target", &pb.ComponentVersion{Current: "v1.14.5"}, "v1.14.5"},
		{"behind", &pb.ComponentVersion{Current: "v1.36.4", Target: "v1.36.6", State: "behind"}, "v1.36.4 (behind: this Ichor ships v1.36.6)"},
		{"differs, empty state", &pb.ComponentVersion{Current: "v1.36.4", Target: "v1.36.6"}, "v1.36.4 (this Ichor ships v1.36.6)"},
	} {
		if got := versionRow(tc.v); got != tc.want {
			t.Errorf("%s: versionRow = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A TalosCluster has no Ready condition: these are the conditions of a healthy one as the controller sets them.
func realConditions() []*pb.Condition {
	return []*pb.Condition{
		cond("CertificatesExpiring", condFalse, "NoneExpiring", "no CA expires within 30 days", time.Minute),
		cond("Transitioning", condFalse, "Settled", "", time.Minute),
		cond("CNIHandover", condTrue, "Complete", "Cilium is handed over", time.Minute),
		cond("CNIValuesApplied", condTrue, "Applied", "", time.Minute),
		cond("CiliumUpgradeAvailable", condFalse, "UpToDate", "", time.Minute),
	}
}

func TestClusterHealthWithoutAReadyCondition(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	for _, tc := range []struct {
		name    string
		mutate  func(*pb.Cluster)
		want    string
		wantRow string
	}{
		{"settled", func(*pb.Cluster) {}, "✓ ready", "✓"},
		{"transitioning", func(cl *pb.Cluster) { cl.Conditions[1] = cond("Transitioning", condTrue, "Rolling", "", time.Minute) }, "… transitioning", "…"},
		{"rolling out", func(cl *pb.Cluster) { cl.Rollout = &pb.RolloutProgress{} }, "… upgrading", "…"},
		{"a health condition is false", func(cl *pb.Cluster) {
			cl.Conditions = append(cl.Conditions, cond("ControlPlaneAvailable", condFalse, "NoQuorum", "", time.Minute))
		}, "! not ready (ControlPlaneAvailable)", "!"},
		{"an advisory condition is true", func(cl *pb.Cluster) { cl.Conditions[4] = cond("CiliumUpgradeAvailable", condTrue, "Newer", "", time.Minute) }, "✓ ready", "✓"},
	} {
		cl := initshowCluster()
		cl.Conditions = realConditions()
		tc.mutate(cl)

		var out bytes.Buffer
		if err := renderClusterStatus(newHuman(&out), cl); err != nil {
			t.Fatal(err)
		}

		if want := "  Health       " + tc.want + "\n"; !strings.Contains(out.String(), want) {
			t.Errorf("%s: status lacks %q:\n%s", tc.name, want, out.String())
		}

		out.Reset()

		if err := renderClusterList(newHuman(&out), &pb.ListClustersResponse{Clusters: []*pb.Cluster{cl}}); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(out.String(), "  "+tc.wantRow+"  ") {
			t.Errorf("%s: list lacks the READY mark %q:\n%s", tc.name, tc.wantRow, out.String())
		}
	}
}

func TestClusterGetDrawsACompletedHandoverAsDone(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	cl := initshowCluster()
	cl.Conditions = realConditions()

	var out bytes.Buffer
	if err := renderClusterGet(newHuman(&out), cl); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"  Health       ✓ ready\n", "  ✓ CNIHandover "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get lacks %q:\n%s", want, out.String())
		}
	}
}
