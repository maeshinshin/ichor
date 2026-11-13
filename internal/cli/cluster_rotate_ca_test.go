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

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// rotate ca goes through OperationService like every other start: there is
// no second path that writes the operation from the kubeconfig.

func TestRotateCAPromptSaysKubeletRestartsAndTheBundleGoesStaleAndDeclineWritesNothing(t *testing.T) {
	setAPITestFlags(t, false)

	c, k8s := freshClients(t, opTestObjects())

	var prompt bytes.Buffer

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{
		"ca-rotation", "2 machine(s)", "1. " + opCP + " (controlplane)", "2. " + testMachineName + " (worker)",
		"Accepting, Issuing, Dropping", "kubelet", "ichor export bundle " + opTestCluster,
		"STOP WORKING from the second pass (Issuing)", "WAITS", "ichor create credentials NAME --acknowledge",
	} {
		if !strings.Contains(prompt.String(), want) && !strings.Contains(strings.ToLower(prompt.String()), strings.ToLower(want)) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	// The Talos API serves the new CA from Issuing too, so the old talosconfig fails there, not at Dropping.
	if strings.Contains(prompt.String(), "third") {
		t.Errorf("prompt still says the Talos API refuses the old CA after the third pass:\n%s", prompt.String())
	}

	if len(opTestOps(t, k8s)) != 0 {
		t.Fatal("a declined rotation wrote something")
	}
}

func TestRotateCAWithYesCreatesOneOperationAndRefusesASecondWhileItRuns(t *testing.T) {
	setAPITestFlags(t, true)

	// The start right is asked as the caller; here Kubernetes says yes.
	k8s := allowStarts(t, opTestObjects())
	c := localClients(apiOpts, apiDeps{K8s: k8s})
	spec := startSpec{Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Cluster: opTestCluster}

	var errw bytes.Buffer
	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw, spec); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindCARotation || ops[0].Spec.ClusterRef.Name != opTestCluster {
		t.Fatalf("ops = %+v", ops)
	}

	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, spec); err == nil || len(opTestOps(t, k8s)) != 1 {
		t.Fatalf("a second rotation was started while one is running: %v", err)
	}
}

func TestRotateCAIsOneCommandThroughTheEntryPoint(t *testing.T) {
	if _, ok := rotateCACmd.Annotations[writesAnnotation]; !ok {
		t.Error("rotate ca is not marked as a write")
	}

	if got := kindName(pb.OperationKind_OPERATION_KIND_CA_ROTATION); got != "ca-rotation" {
		t.Errorf("kindName = %q", got)
	}

	line := progressLine(&pb.Operation{Ref: objectRef(testNamespace, "x"), Kind: pb.OperationKind_OPERATION_KIND_CA_ROTATION, Phase: "InProgress", CaRotationStage: "Issuing"})
	if !strings.Contains(line, "stage Issuing") {
		t.Errorf("progress line lacks the stage: %q", line)
	}
}

// The done hint names the new kubeconfig rotate ca wrote: the old one trusts only the old CA and
// fails with x509 once the rotation is over.
func TestRotateCADoneHintNamesTheNewKubeconfig(t *testing.T) {
	const newKubeconfig = "/home/op/.kube/config.ichor-new"

	var errw bytes.Buffer
	rotateCAAfterwards(&errw, opTestCluster, newKubeconfig)

	want := "ichor create credentials " + opTestCluster + " --kubeconfig " + newKubeconfig + "' for files"
	if !strings.Contains(errw.String(), want) {
		t.Errorf("done hint lacks %q:\n%s", want, errw.String())
	}
}

// The hand-off needs the operator's own kubeconfig, so rotate ca does it with or without --follow
// (a rotation left at AwaitingCredentials waits for it); the help says so instead of surprising anyone.
func TestRotateCAHelpSaysTheHandoffHappensWithoutFollow(t *testing.T) {
	if !strings.Contains(rotateCACmd.Long, "with or without --follow") {
		t.Errorf("the help does not say the hand-off is done with or without --follow:\n%s", rotateCACmd.Long)
	}

	if u := rotateCACmd.Flags().Lookup("follow").Usage; !strings.Contains(u, "without it") {
		t.Errorf("--follow usage = %q", u)
	}
}

func TestProgressLineOfAnOperationWithoutMachinesHasNoCounts(t *testing.T) {
	line := progressLine(&pb.Operation{Ref: objectRef(testNamespace, "x"), Kind: pb.OperationKind_OPERATION_KIND_ICHOR, Phase: ichorv1alpha1.TalosOperationPhaseInProgress})
	if line != "ichor x: phase InProgress" {
		t.Errorf("progress line = %q", line)
	}
}
