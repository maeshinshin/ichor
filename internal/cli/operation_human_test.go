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

	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// Golden tests of the operation, upgrade and node commands' human output, on a pipe.

const (
	opHumanName    = "upgrade-talos-x7k2p"
	opHumanTalos   = "Upgrade Talos v1.14.3 → v1.14.5"
	opHumanFailure = "drain of cp2 timed out: pod etcd-backup cannot be evicted"
)

func opRef(name string) *pb.ObjectRef {
	return &pb.ObjectRef{Namespace: ichorControllerNamespace, Name: name}
}

func opHumanTalosOp(phase string) *pb.Operation {
	return &pb.Operation{
		Ref: opRef(opHumanName), Cluster: opRef(humanCluster), Kind: pb.OperationKind_OPERATION_KIND_TALOS, Phase: phase,
		Summary: opHumanTalos, CreatedAt: ago(12 * time.Minute), StartedBy: actorCLI, MaxUnavailable: 1,
		Details: []*pb.OperationDetail{{Key: "Cluster", Value: humanCluster}, {Key: "Kind", Value: compTalos},
			{Key: "Target Talos version", Value: testTalosVersion}, {Key: "Max unavailable", Value: "1"}},
		Machines: []*pb.OperationMachine{
			{Name: opCP, Phase: ichorv1alpha1.TalosOperationMachinePhaseCompleted, StartedAt: ago(11 * time.Minute), FinishedAt: ago(7*time.Minute + 30*time.Second)},
			{Name: "cp2", DisplayName: "talos-cp2", Phase: ichorv1alpha1.TalosOperationMachinePhaseInProgress, StartedAt: ago(7 * time.Minute)},
			{Name: "w1"},
		},
	}
}

func TestOperationListHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	running := opHumanTalosOp(ichorv1alpha1.TalosOperationPhaseInProgress)
	running.Message = "upgrading talos-cp2 (2 of 3)"

	failed := opHumanTalosOp(condPaused)
	failed.Ref, failed.FailureReason = opRef("upgrade-talos-failed"), opHumanFailure

	reboot := &pb.Operation{
		Ref: opRef("node-reboot-m2"), Cluster: opRef(humanCluster), Kind: pb.OperationKind_OPERATION_KIND_NODE,
		NodeMachine: opRef("talos-cp2"), Phase: ichorv1alpha1.TalosOperationPhaseCompleted, CreatedAt: ago(3 * 24 * time.Hour),
	}
	paused := &pb.Operation{
		Ref: opRef("config-by-hand"), Cluster: opRef(humanCluster), Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Phase: condPaused, CreatedAt: ago(40 * time.Second),
	}
	fresh := &pb.Operation{Ref: opRef("shutdown-new"), Cluster: opRef(humanCluster), Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN}

	var out bytes.Buffer
	if err := renderOperationList(newHuman(&out), &pb.ListOperationsResponse{Operations: []*pb.Operation{running, failed, reboot, paused, fresh}}); err != nil {
		t.Fatal(err)
	}

	want := "NAME                  KIND      PHASE         TARGET     MESSAGE                                                    AGE\n" +
		"upgrade-talos-x7k2p   talos     … InProgress  initshow   upgrading talos-cp2 (2 of 3)                               12m\n" +
		"upgrade-talos-failed  talos     ✗ Paused      initshow   drain of cp2 timed out: pod etcd-backup cannot be evicted  12m\n" +
		"node-reboot-m2        node      ✓ Completed   talos-cp2  -                                                          3d\n" +
		"config-by-hand        config    ! Paused      initshow   -                                                          40s\n" +
		"shutdown-new          shutdown  … Pending     initshow   -                                                          -\n"
	wantText(t, out.String(), want)

	out.Reset()

	if err := renderOperationList(newHuman(&out), &pb.ListOperationsResponse{}); err != nil || out.String() != "no operations\n" {
		t.Errorf("empty list: %q, %v", out.String(), err)
	}
}

func TestOperationGetHumanOutputOfAFailedOperation(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	op := opHumanTalosOp(condPaused)
	op.FailureReason = opHumanFailure
	op.Conditions = []*pb.Condition{cond(condPaused, condTrue, "StepFailed", opHumanFailure, time.Minute)}

	var out bytes.Buffer
	if err := renderOperationGet(newHuman(&out), &pb.GetOperationResponse{Operation: op}); err != nil {
		t.Fatal(err)
	}

	want := `upgrade-talos-x7k2p · ichor-system
  Kind         talos
  Summary      Upgrade Talos v1.14.3 → v1.14.5
  Cluster      initshow
  Phase        ✗ Paused
  Failure      drain of cp2 timed out: pod etcd-backup cannot be evicted
  Started by   ichor-cli
  Created      12m ago

Details
  Target Talos version v1.14.5
  Max unavailable      1

Machines
MACHINE  NODE       PHASE         STEP  TIME   MESSAGE
cp1      -          ✓ Completed   -     3m30s  -
cp2      talos-cp2  ✗ InProgress  -     7m00s  -
w1       -          …             -     -      -

Conditions
  ! Paused  StepFailed  drain of cp2 timed out: pod etcd-backup cannot be evicted
`
	wantText(t, out.String(), want)
}

func TestOperationGetHumanOutputOfARunningOperation(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	op := opHumanTalosOp(ichorv1alpha1.TalosOperationPhaseInProgress)
	op.Message, op.Machines = "upgrading talos-cp2", op.Machines[:2]

	var out bytes.Buffer
	if err := renderOperationGet(newHuman(&out), &pb.GetOperationResponse{Operation: op}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"  Phase        … InProgress\n", "  Message      upgrading talos-cp2\n", "cp2      talos-cp2  … InProgress  -     7m00s  -\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	if strings.Contains(out.String(), "Failure") || strings.Contains(out.String(), "Finished") {
		t.Errorf("a running operation has neither:\n%s", out.String())
	}
}

func TestOperationCreatedPausedAndDeletedHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	op := opHumanTalosOp("")
	op.Machines, op.Details, op.CreatedAt = nil, nil, nil

	var out bytes.Buffer
	if err := renderOperationCreated(newHuman(&out), &pb.CreateOperationResponse{Operation: op, SpecChanged: true}); err != nil {
		t.Fatal(err)
	}

	want := `Started upgrade-talos-x7k2p · ichor-system
  Kind         talos
  Summary      Upgrade Talos v1.14.3 → v1.14.5
  Cluster      initshow
  Phase        … Pending
  Spec         changed to the target first
  Follow       ichor describe operation upgrade-talos-x7k2p --watch
`
	wantText(t, out.String(), want)

	out.Reset()

	op.Paused, op.Phase = true, ichorv1alpha1.TalosOperationPhaseInProgress
	if err := renderOperationPaused(newHuman(&out), &pb.SetOperationPausedResponse{Operation: op}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"upgrade-talos-x7k2p · ichor-system\n", "  State        paused\n", "  Paused       yes\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("paused: missing %q in:\n%s", want, out.String())
		}
	}
}

func TestUpgradeTargetsHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	var out bytes.Buffer

	if err := printUpgradeTargets(&out, pb.OperationKind_OPERATION_KIND_TALOS, &pb.ListUpgradeTargetsResponse{
		Available: true, Recommended: []string{testTalosVersion}, Patch: []string{"v1.14.4", "v1.14.3"}, NextMinor: []string{"v1.15.0"}, Default: testTalosVersion,
	}); err != nil {
		t.Fatal(err)
	}

	want := "VERSION  KIND         DEFAULT\n" +
		"v1.14.5  recommended  ✓\n" +
		"v1.14.4  patch        -\n" +
		"v1.14.3  patch        -\n" +
		"v1.15.0  next-minor   -\n"
	wantText(t, out.String(), want)

	out.Reset()

	if err := printUpgradeTargets(&out, pb.OperationKind_OPERATION_KIND_KUBERNETES, &pb.ListUpgradeTargetsResponse{UnavailableReason: "the update check is off"}); err != nil {
		t.Fatal(err)
	}

	wantText(t, out.String(), "! no list of kubernetes versions: the update check is off\nno version to move to\n")

	out.Reset()

	if err := printUpgradeTargets(&out, pb.OperationKind_OPERATION_KIND_TALOS, &pb.ListUpgradeTargetsResponse{Available: true}); err != nil {
		t.Fatal(err)
	}

	wantText(t, out.String(), "✓ talos is on the newest release this Ichor allows\n")
}

// -o yaml and -o json print the wire message, for --list too: machine output, not the human text.
func TestMachineFormatsOfTheOperationCommandsAreUnchanged(t *testing.T) {
	setHumanTestFlags(t, outputYAML)

	var out bytes.Buffer

	if err := printUpgradeTargets(&out, pb.OperationKind_OPERATION_KIND_TALOS, &pb.ListUpgradeTargetsResponse{
		Available: true, Recommended: []string{testTalosVersion}, Patch: []string{"v1.14.4"}, Default: testTalosVersion,
	}); err != nil {
		t.Fatal(err)
	}

	if want := "available: true\ndefault: v1.14.5\npatch:\n- v1.14.4\nrecommended:\n- v1.14.5\n"; out.String() != want {
		t.Errorf("upgrade --list -o yaml: %q, want %q", out.String(), want)
	}

	op := &pb.Operation{Ref: opRef("x"), Phase: ichorv1alpha1.TalosOperationPhaseCompleted}

	out.Reset()

	if err := printAPI(&out, &pb.GetOperationResponse{Operation: op}, renderOperationGet); err != nil {
		t.Fatal(err)
	}

	if want := "operation:\n  phase: Completed\n  ref:\n    name: x\n    namespace: ichor-system\n"; out.String() != want {
		t.Errorf("describe operation -o yaml: %q, want %q", out.String(), want)
	}

	outputFormat = outputJSON

	out.Reset()

	if err := printAPI(&out, &pb.ListOperationsResponse{Operations: []*pb.Operation{op}}, renderOperationList); err != nil {
		t.Fatal(err)
	}

	if want := "{\n  \"operations\": [\n    {\n      \"ref\": {\n        \"namespace\": \"ichor-system\",\n        \"name\": \"x\"\n      },\n      \"phase\": \"Completed\"\n    }\n  ]\n}\n"; out.String() != want {
		t.Errorf("get operations -o json: %q, want %q", out.String(), want)
	}

	out.Reset()

	if err := printAPI(&out, &pb.CreateOperationResponse{Operation: op, SpecChanged: true}, renderOperationCreated); err != nil {
		t.Fatal(err)
	}

	if want := "{\n  \"operation\": {\n    \"ref\": {\n      \"namespace\": \"ichor-system\",\n      \"name\": \"x\"\n    },\n    \"phase\": \"Completed\"\n  },\n  \"specChanged\": true\n}\n"; out.String() != want {
		t.Errorf("start -o json: %q, want %q", out.String(), want)
	}
}

func useWatchClock(t *testing.T) *fakeClock {
	t.Helper()

	clk := &fakeClock{t: humanTestNow}
	old := watchClock
	watchClock = clk.now

	t.Cleanup(func() { watchClock = old })

	return clk
}

// A live watch shows one step whose detail changes, and ends it with the phase and the time. On a
// pipe a line is printed when the detail changes.
func TestWatchHumanShowsOneStepAndEndsItWithTheTime(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	clk := useWatchClock(t)

	var errw bytes.Buffer

	w := newOpWatch(&errw)

	op := opHumanTalosOp(ichorv1alpha1.TalosOperationPhaseInProgress)
	op.CreatedAt, op.Message = timestamppb.New(clk.now().Add(-time.Minute)), "upgrading cp1"
	op.Machines = op.Machines[:1]
	op.Machines[0].Phase = ichorv1alpha1.TalosOperationMachinePhaseInProgress

	w.show(op)
	clk.advance(3 * time.Second)
	w.show(op) // the same detail again, held back

	clk.advance(2 * time.Minute)
	op.Message, op.Machines[0].Phase = "upgrading cp2", ichorv1alpha1.TalosOperationMachinePhaseCompleted
	w.show(op)

	clk.advance(2 * time.Minute)
	op.Phase, op.Message, op.FinishedAt = ichorv1alpha1.TalosOperationPhaseCompleted, "", timestamppb.New(clk.now())
	w.show(op)
	w.end(op)

	want := "Upgrade Talos v1.14.3 → v1.14.5\n" +
		"operation upgrade-talos-x7k2p · cluster initshow\n" +
		"… talos — InProgress · 0/1 machines · upgrading cp1\n" +
		"… talos — InProgress · 1/1 machines · upgrading cp2\n" +
		"✓ talos — Completed (5m03s)\n"
	wantText(t, errw.String(), want)
}

func TestWatchHumanEndsFailedAndPausedByTheOperator(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	useWatchClock(t)

	var errw bytes.Buffer

	w := newOpWatch(&errw)

	op := opHumanTalosOp(condPaused)
	op.CreatedAt, op.Machines = nil, nil

	w.show(op) // an operator's pause is waited out
	op.FailureReason = opHumanFailure
	w.show(op)
	w.end(op)

	want := "Upgrade Talos v1.14.3 → v1.14.5\n" +
		"operation upgrade-talos-x7k2p · cluster initshow\n" +
		"… talos — Paused · resume with `ichor resume operation upgrade-talos-x7k2p`\n" +
		"✗ talos — Failed\n"
	wantText(t, errw.String(), want)
}

func TestWatchHumanShowsTheCARotationStagesAPollJumpedOver(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	useWatchClock(t)

	var errw bytes.Buffer

	w := newOpWatch(&errw)

	op := &pb.Operation{
		Ref: opRef("rotate"), Cluster: opRef(humanCluster), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION,
		Phase: ichorv1alpha1.TalosOperationPhaseInProgress, CaRotationStage: ichorv1alpha1.CARotationStageAccepting,
	}
	w.show(op)

	op.CaRotationStage = ichorv1alpha1.CARotationStageIssuing
	w.show(op)

	op.Phase = ichorv1alpha1.TalosOperationPhaseCompleted
	w.show(op)
	w.end(op)

	for _, want := range []string{"stage " + ichorv1alpha1.CARotationStageAccepting, "stage " + ichorv1alpha1.CARotationStageAwaitingCredentials,
		"stage " + ichorv1alpha1.CARotationStageIssuing} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("missing %q in:\n%s", want, errw.String())
		}
	}
}

// On a terminal the step is one live line redrawn in place, and its end line replaces it.
func TestWatchHumanOnATerminalRedrawsOneLine(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	p, buf, clock := newTestProgress(true, false, 100)
	w := &opWatch{p: p}

	op := opHumanTalosOp(ichorv1alpha1.TalosOperationPhaseInProgress)
	op.CreatedAt, op.Message = timestamppb.New(clock.now().Add(-time.Minute)), "upgrading cp1"

	w.show(op)
	clock.advance(time.Second)

	op.Phase, op.Message, op.FinishedAt = ichorv1alpha1.TalosOperationPhaseCompleted, "", timestamppb.New(clock.now())
	w.show(op)
	w.end(op)

	got := buf.String()
	if !strings.Contains(got, "\r\x1b[2K") || !strings.Contains(got, "InProgress · 1/3 machines") || !strings.Contains(got, "✓ talos") ||
		!strings.Contains(got, "Completed") || !strings.HasSuffix(got, "\n") {
		t.Errorf("terminal output: %q", got)
	}
}

// setWatchInterval polls an operation every d for the rest of the test.
func setWatchInterval(t *testing.T, d time.Duration) {
	t.Helper()

	old := opWatchInterval
	opWatchInterval = d

	t.Cleanup(func() { opWatchInterval = old })
}

// watchClients is the in-process server over op and objs; op's status is kept.
func watchClients(t *testing.T, op *ichorv1alpha1.TalosOperation, objs ...client.Object) (*apiClients, client.Client) {
	t.Helper()

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(objs, op)...).WithStatusSubresource(op).Build()

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

func watchOpFixture(phase, failure string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{}
	op.Name, op.Namespace = "x", testNamespace
	op.CreationTimestamp = metav1.NewTime(humanTestNow.Add(-5 * time.Minute))
	op.Spec.ClusterRef.Name, op.Spec.Kind = opTestCluster, ichorv1alpha1.TalosOperationKindTalos
	op.Status = ichorv1alpha1.TalosOperationStatus{
		Phase: phase, FailureReason: failure,
		Machines: []ichorv1alpha1.TalosOperationMachineStatus{{Name: opCP, Phase: ichorv1alpha1.TalosOperationMachinePhaseCompleted}, {Name: "w", Phase: ichorv1alpha1.TalosOperationMachinePhaseInProgress}},
	}

	return op
}

// The whole watch: the progress on stderr, the final get block on stdout, the failure as the error
// with the resume hint, as ever.
func TestWatchHumanFailedOperationReturnsTheResumeHintAndPrintsTheBlock(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	useWatchClock(t)

	setWatchInterval(t, time.Millisecond)

	op := watchOpFixture(condPaused, "boom")
	c, _ := watchClients(t, op)

	var out, errw bytes.Buffer

	err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "operation x paused after a failure: boom (fix the cause, then `ichor resume operation x`)") {
		t.Fatalf("want the failure with the resume hint, got %v", err)
	}

	if errorShown(err) {
		t.Errorf("the hint must be printed by Execute: %v", err)
	}

	wantText(t, errw.String(), "Upgrade Talos\noperation x · cluster adopt-cluster\n✗ talos — Failed (5m00s)\n")

	for _, want := range []string{"x · default\n", "  Failure      boom\n", "cp1      ", "w        "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, out.String())
		}
	}
}

func TestWatchHumanCompletedOperationExitsCleanly(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	useWatchClock(t)

	op := watchOpFixture(ichorv1alpha1.TalosOperationPhaseCompleted, "")
	c, _ := watchClients(t, op)

	var out, errw bytes.Buffer

	if err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw); err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(errw.String(), "✓ talos — Completed (5m00s)\n") || !strings.Contains(out.String(), "  Phase        ✓ Completed\n") {
		t.Errorf("stderr:\n%s\nstdout:\n%s", errw.String(), out.String())
	}
}

func TestWatchHumanInterruptedLeavesTheOperationRunning(t *testing.T) {
	setHumanTestFlags(t, outputHuman)
	useWatchClock(t)

	setWatchInterval(t, time.Hour)

	op := watchOpFixture(ichorv1alpha1.TalosOperationPhaseInProgress, "")
	c, _ := watchClients(t, op)

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	var out, errw bytes.Buffer

	if err := watchOperation(ctx, c, objectRef(testNamespace, "x"), &out, &errw); err == nil {
		t.Fatal("an interrupted follow is an error exit")
	}

	if !strings.Contains(errw.String(), "! talos — Stopped following; the operation keeps running") {
		t.Errorf("stderr:\n%s", errw.String())
	}
}

func TestNodeLiveHumanOutput(t *testing.T) {
	setHumanTestFlags(t, outputHuman)

	n := &pb.NodeDetail{
		Machine:  opRef("talos-cp2"),
		Hardware: &pb.Hardware{Manufacturer: "QEMU", Model: "Standard PC", CpuCores: 4, MemoryTotalBytes: 8 << 30},
		Disks: []*pb.Disk{
			{DevPath: classADisk, Serial: "disk-1", SizeBytes: 40 << 30, Role: pb.DiskRole_DISK_ROLE_SYSTEM},
			{DevPath: "/dev/vdb", SizeBytes: 500 << 30, Role: pb.DiskRole_DISK_ROLE_UNUSED},
		},
		ResourceUsage: &pb.ResourceUsage{
			CpuSampled: true, CpuCoresUsed: 0.25, MemoryUsedBytes: 3 << 29, MemoryTotalBytes: 8 << 30,
			FilesystemUsedBytes: 700 << 20, FilesystemTotalBytes: 38 << 30,
		},
		Kubernetes: &pb.KubernetesNodeStatus{NodeName: "talos-cp2", Ready: true, KubeletVersion: humanK8s, Schedulable: false},
	}

	var out bytes.Buffer
	if err := renderNodeLive(newHuman(&out), n); err != nil {
		t.Fatal(err)
	}

	want := `Hardware
  Manufacturer QEMU
  Model        Standard PC
  CPU          4 cores (0.25 in use)
  Memory       1.5 GiB / 8.0 GiB
  Filesystem   700 MiB / 38.0 GiB

Kubernetes
  Node         talos-cp2
  Ready        ✓ ready
  Kubelet      v1.36.6
  Schedulable  no

Live disks
DISK      ROLE    SIZE       ID
/dev/vda  system  40.0 GiB   disk-1
/dev/vdb  unused  500.0 GiB  -
`
	wantText(t, out.String(), want)

	out.Reset()

	n.Kubernetes, n.KubernetesPermissionDenied = nil, true
	if err := renderNodeLive(newHuman(&out), n); err != nil || !strings.Contains(out.String(), "Kubernetes\n  Node         you may not read the Kubernetes Node\n") {
		t.Errorf("permission denied: %q, %v", out.String(), err)
	}
}

func TestNodeActionHumanOutput(t *testing.T) {
	c, k8s := nodeActionClients(t, true)
	outputFormat = outputHuman

	fixedHumanClock(t)

	var out, errw bytes.Buffer

	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REBOOT}); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 {
		t.Fatalf("operations = %+v", ops)
	}

	for _, want := range []string{"Started " + ops[0].Name + " · default\n", "  Kind         node\n", "  Follow       ichor describe operation " + ops[0].Name + " --watch\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, out.String())
		}
	}

	c, _ = nodeActionClients(t, true) // a node cannot be removed while an operation of its cluster runs
	outputFormat = outputHuman

	out.Reset()
	errw.Reset()

	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName}); err != nil {
		t.Fatal(err)
	}

	wantText(t, out.String(), "✓ removing node "+testMachineName+": the controller drains it, removes its etcd member (control plane), wipes it and deletes its Kubernetes Node\n")

	for _, want := range []string{"Next\n", "new pending node", "powers it off"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("remove stderr lacks %q:\n%s", want, errw.String())
		}
	}
}

// -o yaml of the node actions and `delete operation` is the wire message, as before.
func TestNodeActionAndOperationDeleteMachineFormatsAreUnchanged(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	outputFormat = outputYAML

	var out, errw bytes.Buffer

	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &out, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName}); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(out.String(), "audit:\n") || !strings.Contains(errw.String(), "\nNext: The controller drains the node") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out.String(), errw.String())
	}
}
