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
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/maeshinshin/ichor/pkg/cni"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/release"
)

const (
	opTestCluster = "adopt-cluster"
	// opOldVersion is the Talos the test cluster starts behind.
	opOldVersion = "v1.13.2"
	opCP         = "cp1"
)

// opNewVersion is the release set's Talos: the only target an upgrade accepts.
var opNewVersion = release.Pinned.Talos

func opTestObjects() []client.Object {
	cp := newTestMachine(opTestCluster)
	cp.Name, cp.Spec.Role = opCP, roleControlPlane

	cluster := newTestCluster()
	cluster.Spec.TalosVersion, cluster.Spec.VersionContract = opOldVersion, ver113

	return []client.Object{cluster, cp, newTestMachine(opTestCluster)}
}

func opTestOps(t *testing.T, k8s client.Client) []ichorv1alpha1.TalosOperation {
	t.Helper()

	var l ichorv1alpha1.TalosOperationList
	if err := k8s.List(context.Background(), &l); err != nil {
		t.Fatal(err)
	}

	return l.Items
}

func opTestVersion(t *testing.T, k8s client.Client) string {
	t.Helper()

	var c ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: opTestCluster}, &c); err != nil {
		t.Fatal(err)
	}

	return c.Spec.TalosVersion
}

func TestUpgradeTalosAsksWhichMachinesRebootInWhichOrderAndDeclineWritesNothing(t *testing.T) {
	setAPITestFlags(t, false)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var prompt bytes.Buffer

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewVersion})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{"REBOOTS", "2 machine(s)", "1. " + opCP + " (controlplane)", "2. " + testMachineName + " (worker)", opOldVersion, opNewVersion} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	if opTestVersion(t, k8s) != opOldVersion || len(opTestOps(t, k8s)) != 0 {
		t.Fatal("a declined upgrade wrote something")
	}
}

func TestUpgradeTalosWithYesPrintsThePlanSkipsThePromptAndWritesSpecThenOperation(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out, prompt bytes.Buffer
	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &out, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewVersion}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(prompt.String(), "Proceed?") {
		t.Fatalf("--yes must not prompt: %q", prompt.String())
	}

	if !strings.Contains(prompt.String(), "REBOOTS") || !strings.Contains(prompt.String(), opNewVersion) {
		t.Fatalf("--yes must still print the plan: %q", prompt.String())
	}

	ops := opTestOps(t, k8s)
	if opTestVersion(t, k8s) != opNewVersion || len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindTalos {
		t.Fatalf("version=%s ops=%+v", opTestVersion(t, k8s), ops)
	}

	if !strings.Contains(out.String(), actorCLI) {
		t.Fatalf("the audit record must name the CLI path: %s", out.String())
	}
}

func TestStartThroughTheBackendAndWithoutItDoTheSame(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(uiBackendObjects(), opTestObjects()...)...).Build()

	var forwards atomic.Int32

	c, err := connectAPI(context.Background(), apiOpts, backendFor(t, k8s, &forwards, nil), false)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if c.Local {
		t.Fatal("expected the backend")
	}

	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CNI, Target: kubeProxyReplaced}); err != nil {
		t.Fatal(err)
	}

	var cl ichorv1alpha1.TalosCluster
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: opTestCluster}, &cl); err != nil {
		t.Fatal(err)
	}

	if cl.Spec.CNI.KubeProxy != "replaced" || cl.Annotations[ichorv1alpha1.TalosClusterConfirmKubeProxySwitchAnnotation] != "" {
		t.Fatalf("cluster = %+v", cl)
	}

	if len(opTestOps(t, k8s)) != 1 {
		t.Fatal("no operation")
	}
}

func TestShutdownPromptSaysEveryMachineIsPoweredOff(t *testing.T) {
	setAPITestFlags(t, false)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var prompt bytes.Buffer

	_ = runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_SHUTDOWN, Cluster: opTestCluster})

	for _, want := range []string{"SHUT DOWN", "POWERED OFF", "1. " + testMachineName + " (worker)", "2. " + opCP + " (controlplane)"} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}
}

func TestStartNeedsAClusterNameWhenThereIsNotExactlyOne(t *testing.T) {
	setAPITestFlags(t, true)

	second := newTestCluster()
	second.Name = "other"
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(opTestObjects(), second)...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewVersion})
	if err == nil || !strings.Contains(err.Error(), "--cluster") {
		t.Fatalf("want a --cluster hint, got %v", err)
	}
}

func TestOperationDeleteAsksUnlessYes(t *testing.T) {
	setAPITestFlags(t, false)

	op := &ichorv1alpha1.TalosOperation{}
	op.Name, op.Namespace = "x", testNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = opTestCluster, ichorv1alpha1.TalosOperationKindTalos

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(opTestObjects(), op)...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	if err := runOperationDelete(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, "x"); err == nil || len(opTestOps(t, k8s)) != 1 {
		t.Fatalf("declined delete: err=%v", err)
	}

	if err := runOperationDelete(context.Background(), c, strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, "x"); err != nil || len(opTestOps(t, k8s)) != 0 {
		t.Fatalf("confirmed delete: err=%v", err)
	}
}

func TestWatchEndsWithAnErrorWhenAMachineFailedAndPrintsProgress(t *testing.T) {
	setAPITestFlags(t, true)

	old := opWatchInterval
	opWatchInterval = time.Millisecond

	t.Cleanup(func() { opWatchInterval = old })

	op := &ichorv1alpha1.TalosOperation{}
	op.Name, op.Namespace = "x", testNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = opTestCluster, ichorv1alpha1.TalosOperationKindTalos
	op.Status = ichorv1alpha1.TalosOperationStatus{
		Phase: "Paused", FailureReason: "boom",
		Machines: []ichorv1alpha1.TalosOperationMachineStatus{{Name: "cp1", Phase: "Completed"}, {Name: "w", Phase: "InProgress"}},
	}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(op).WithStatusSubresource(op).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out, errw bytes.Buffer

	err := watchOperation(context.Background(), c, objectRef(testNamespace, "x"), &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want the failure as the error, got %v", err)
	}

	if !strings.Contains(errw.String(), "1 done, 1 in progress") || !strings.Contains(out.String(), "failureReason") {
		t.Fatalf("progress=%q out=%q", errw.String(), out.String())
	}
}

func TestStartCommandsAreMarkedAsWrites(t *testing.T) {
	for name, ann := range map[string]string{
		"talos": upgradeTalosCmd.Annotations[writesAnnotation], "kubernetes": upgradeKubernetesCmd.Annotations[writesAnnotation],
		"ichor": upgradeIchorCmd.Annotations[writesAnnotation], "kube-proxy": cniKubeProxyCmd.Annotations[writesAnnotation],
		"shutdown": clusterShutdownCmd.Annotations[writesAnnotation], "pause": operationPauseCmd.Annotations[writesAnnotation],
		"resume": operationResumeCmd.Annotations[writesAnnotation],
		"delete": operationDeleteCmd.Annotations[writesAnnotation],
	} {
		if ann != "true" {
			t.Errorf("%s is not marked as a write", name)
		}
	}
}

func TestPlanTextWarnsWhenTheKubernetesAPIGoesDown(t *testing.T) {
	p := &pb.PlanOperationResponse{
		Effect:                   pb.OperationEffect_OPERATION_EFFECT_REBOOT,
		KubernetesApiUnavailable: true,
		Machines:                 []*pb.PlannedMachine{{Name: opCP, Role: roleControlPlane, Step: 1}},
	}

	got := planText(pb.OperationKind_OPERATION_KIND_CONFIG, "c", p)
	for _, want := range []string{"Ichor cordons and drains it first", "Kubernetes API is UNAVAILABLE"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "Talos cordons") {
		t.Errorf("text still says Talos cordons and drains on a reboot:\n%s", got)
	}

	p.KubernetesApiUnavailable = false
	if strings.Contains(planText(pb.OperationKind_OPERATION_KIND_CONFIG, "c", p), "UNAVAILABLE") {
		t.Error("the warning appears without the flag")
	}
}

func TestPlanTextForAConfigRestartSaysNoReboot(t *testing.T) {
	p := &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, MaxUnavailable: 1,
		Machines: []*pb.PlannedMachine{{Name: opCP, Role: roleControlPlane, Step: 1}},
	}

	got := planText(pb.OperationKind_OPERATION_KIND_CONFIG, "c", p)
	if !strings.Contains(got, "Restarts the Kubernetes API server on each control plane, one at a time; no reboot.") {
		t.Errorf("text lacks the restart sentence:\n%s", got)
	}

	if strings.Contains(got, "REBOOTS") {
		t.Errorf("text says the machines reboot:\n%s", got)
	}
}

func TestHeadlineForAKubeProxySwitchSaysItStartsWhenTheSpecAlreadySaysTheTarget(t *testing.T) {
	// The second run of `ichor cni kube-proxy replaced --yes`: the spec already says replaced.
	mode := string(cni.KubeProxyReplaced)
	same := &pb.PlanOperationResponse{Current: mode, Target: mode}

	want := `Switch kube-proxy on cluster "c": starting the switch to ` + mode + " (the spec already says " + mode + ")."
	if got := headline(pb.OperationKind_OPERATION_KIND_CNI, "c", same); got != want {
		t.Errorf("headline = %q, want %q", got, want)
	}

	// A first run that changes the spec still says what it changes.
	change := &pb.PlanOperationResponse{Current: kubeProxyEnabled, Target: mode}
	if got, want := headline(pb.OperationKind_OPERATION_KIND_CNI, "c", change), `Switch kube-proxy on cluster "c" from enabled to `+mode+"."; got != want {
		t.Errorf("headline = %q, want %q", got, want)
	}
}

func TestPlanTextForTalosNamesASchematicOnlyChange(t *testing.T) {
	p := &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_REBOOT, Current: "v1.14.3", MaxUnavailable: 1,
		CurrentSchematic: "aaaa", TargetSchematic: "bbbb",
		Machines: []*pb.PlannedMachine{{Name: opCP, Role: roleControlPlane, Step: 1}},
	}

	got := planText(pb.OperationKind_OPERATION_KIND_TALOS, "c", p)
	if !strings.Contains(got, "keeping v1.14.3: schematic aaaa → bbbb") {
		t.Errorf("text does not name the schematic change:\n%s", got)
	}

	p.CurrentSchematic, p.TargetSchematic = "", ""
	if strings.Contains(planText(pb.OperationKind_OPERATION_KIND_TALOS, "c", p), "schematic") {
		t.Error("the schematic text appears without a schematic change")
	}
}

func TestPlanTextForKubernetesNamesTheStagesAndCarriesTheWarning(t *testing.T) {
	p := &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, Current: "v1.36.0", Target: "v1.37.0", MaxUnavailable: 1,
		Stages:   []string{"apiservers", "controller-manager and scheduler", "kube-proxy and the bootstrap manifests", "kubelets"},
		Warning:  "The cluster endpoint is the address of a single control plane.",
		Machines: []*pb.PlannedMachine{{Name: opCP, Role: roleControlPlane, Step: 1}, {Name: "w1", Role: testRoleWorker, Step: 2}},
	}

	got := planText(pb.OperationKind_OPERATION_KIND_KUBERNETES, "c", p)
	for _, want := range []string{
		"in 4 stages", "1. apiservers", "2. controller-manager and scheduler", "3. kube-proxy and the bootstrap manifests", "4. kubelets",
		"newer than an API server", "Warning: The cluster endpoint is the address of a single control plane.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

func TestPlanTextMentionsHeldMachinesAndTheBlastRadius(t *testing.T) {
	p := &pb.PlanOperationResponse{
		Effect: pb.OperationEffect_OPERATION_EFFECT_RESTART_COMPONENTS, Current: "v1.36.0", Target: "v1.36.1", MaxUnavailable: 2,
		Held: []string{"w9"},
		Machines: []*pb.PlannedMachine{
			{Name: opCP, Role: roleControlPlane, Step: 1}, {Name: "w1", Role: testRoleWorker, Step: 2}, {Name: "w2", Role: testRoleWorker, Step: 2},
		},
	}

	got := planText(pb.OperationKind_OPERATION_KIND_KUBERNETES, "c", p)
	for _, want := range []string{"without a reboot", "3 machine(s) (1 control plane, 2 worker)", "at most 2 at once", "2. w1, w2 (worker)", "Held", "w9", "nothing is rolled back"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
}

func failedOpClients(t *testing.T) (*apiClients, client.Client) {
	t.Helper()
	setAPITestFlags(t, true)

	op := &ichorv1alpha1.TalosOperation{}
	op.Name, op.Namespace = "x", testNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = opTestCluster, ichorv1alpha1.TalosOperationKindTalos
	op.Status = ichorv1alpha1.TalosOperationStatus{Phase: "Paused", FailureReason: "drain blocked"}

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(append(opTestObjects(), op)...).WithStatusSubresource(op).Build()

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

func TestResumeOfAFailedOperationReRunsTheStep(t *testing.T) {
	c, k8s := failedOpClients(t)

	var out, errw bytes.Buffer

	if err := runSetPaused(context.Background(), c, &out, &errw, "x", false); err != nil {
		t.Fatal(err)
	}

	if want := "re-running the step that failed: drain blocked"; !strings.Contains(errw.String(), want) {
		t.Fatalf("stderr = %q, want %q", errw.String(), want)
	}

	if got := opTestOps(t, k8s)[0].Annotations[ichorv1alpha1.OperationRetryAnnotation]; got == "" {
		t.Fatal("the retry annotation was not set")
	}
}

func TestInProcessOperationIsStartedByTheCallersName(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s, actor: "alice@example.com"})

	err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_TALOS, Target: opNewVersion})
	if err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Annotations[ichorv1alpha1.OperationStartedByAnnotation] != "alice@example.com" {
		t.Fatalf("ops = %+v", ops)
	}
}
