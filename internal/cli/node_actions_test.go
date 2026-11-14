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

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func nodeActionClients(t *testing.T, yes bool) (*apiClients, client.Client) {
	t.Helper()

	setAPITestFlags(t, yes)
	apiOpts.Override = true // a remove needs it

	k8s := reviewAs(true, fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build())

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

// present reports whether the machine is still there.
func present(k8s client.Client, name string) bool {
	var m ichorv1alpha1.TalosMachine

	return k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &m) == nil
}

func TestNodeRebootShowsThePlanAndDeclineWritesNothing(t *testing.T) {
	c, k8s := nodeActionClients(t, false)

	var prompt bytes.Buffer

	err := runNodeAction(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REBOOT})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("declined = %v", err)
	}

	for _, want := range []string{"Reboot node", "Cordon the node", "Warning:", "Proceed? [y/N]"} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	if ops := opTestOps(t, k8s); len(ops) != 0 {
		t.Fatalf("a declined plan wrote %+v", ops)
	}
}

// --yes skips only the question: the plan is still printed and one node operation is written.
func TestNodeRebootYesCreatesOneNodeOperation(t *testing.T) {
	c, k8s := nodeActionClients(t, true)

	var errw bytes.Buffer

	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REBOOT}); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindNode ||
		ops[0].Spec.Node.MachineRef.Name != testMachineName || ops[0].Spec.Node.Action != ichorv1alpha1.NodeActionReboot {
		t.Fatalf("operations = %+v", ops)
	}

	if !strings.Contains(errw.String(), "Reboot node") || strings.Contains(errw.String(), "Proceed?") {
		t.Fatalf("--yes must print the plan and skip only the question:\n%s", errw.String())
	}
}

func TestNodeRestartServiceAndEtcdRefusalsAreStatedBeforeAnyPrompt(t *testing.T) {
	c, k8s := nodeActionClients(t, true)

	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_RESTART_SERVICE, Service: "etcd"})
	if err == nil || !strings.Contains(err.Error(), "cannot be restarted") {
		t.Fatalf("etcd service = %v", err)
	}

	err = runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_ETCD_DEFRAG})
	if err == nil || !strings.Contains(err.Error(), "only control planes") {
		t.Fatalf("defrag on a worker = %v", err)
	}

	if ops := opTestOps(t, k8s); len(ops) != 0 {
		t.Fatalf("wrote %+v", ops)
	}
}

func TestNodeRemoveNeedsOverride(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	apiOpts.Override = false

	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName})
	if err == nil || !strings.Contains(err.Error(), "--override") {
		t.Fatalf("remove without override = %v", err)
	}
}

func TestNodeRemoveNeedsTheTypedName(t *testing.T) {
	c, k8s := nodeActionClients(t, false)
	ctx := context.Background()

	var prompt bytes.Buffer

	err := runNodeAction(ctx, c, strings.NewReader("wrong\n"), &bytes.Buffer{}, &prompt,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE})
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("wrong name = %v", err)
	}

	if !strings.Contains(prompt.String(), "Type the node name") || !present(k8s, testMachineName) {
		t.Fatalf("a wrong name must remove nothing:\n%s", prompt.String())
	}

	var done bytes.Buffer
	if err := runNodeAction(ctx, c, strings.NewReader(testMachineName+"\n"), &bytes.Buffer{}, &done,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE}); err != nil {
		t.Fatalf("typed name: %v", err)
	}

	if !strings.Contains(done.String(), "deletes its Kubernetes Node") || !strings.Contains(done.String(), "new pending node") {
		t.Fatalf("remove must say how to add the machine again:\n%s", done.String())
	}

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testMachineName}, &m); err == nil && m.DeletionTimestamp.IsZero() {
		t.Fatalf("the machine must be deleted or marked for deletion: %+v", m.ObjectMeta)
	}
}

func TestNodeRemoveWithYesStillNeedsConfirm(t *testing.T) {
	c, k8s := nodeActionClients(t, true)

	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE})
	if err == nil || !strings.Contains(err.Error(), "--confirm") || !present(k8s, testMachineName) {
		t.Fatalf("--yes alone must not remove: %v", err)
	}
}

// --confirm NAME is the typed name given up front: like typing it at the prompt it is the
// consent, so it does not also ask "Proceed? [y/N]" (restore etcd and reclaim node treat it alike).
func TestNodeRemoveWithConfirmDoNotAskAgain(t *testing.T) {
	c, _ := nodeActionClients(t, false)
	apiOpts.Override = true

	var errw bytes.Buffer

	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName})
	if err != nil && (strings.Contains(err.Error(), "aborted") || strings.Contains(err.Error(), "reading confirmation")) {
		t.Fatalf("--confirm alone must be enough: %v\n%s", err, errw.String())
	}

	if strings.Contains(errw.String(), "Proceed?") || errw.Len() == 0 {
		t.Fatalf("the plan is shown and nothing is asked:\n%s", errw.String())
	}
}

// ichor node reset is gone: delete node --reboot replaces it.
func TestNodeResetIsRemoved(t *testing.T) {
	if deleteNodeCmd.Flags().Lookup("reboot") == nil {
		t.Error("ichor delete node has no --reboot")
	}

	for _, c := range deleteCmd.Commands() {
		if c.Name() == "reset" {
			t.Errorf("ichor reset is still registered: %q", c.Short)
		}
	}
}

// NODE may be the hostname, but the typed confirmation is always the machine name: the prompt and the
// refusal name both, and the hostname does not confirm.
func TestNodeRemoveConfirmationIsTheMachineNameNotTheHostname(t *testing.T) {
	const host = "host-a"

	c, k8s := nodeActionClients(t, false)
	ctx := context.Background()

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testMachineName}, &m); err != nil {
		t.Fatal(err)
	}

	m.Spec.Hostname = host
	if err := k8s.Update(ctx, &m); err != nil {
		t.Fatal(err)
	}

	spec := nodeActionSpec{Machine: host, Action: pb.NodeAction_NODE_ACTION_REMOVE}

	var prompt bytes.Buffer

	err := runNodeAction(ctx, c, strings.NewReader(host+"\n"), &bytes.Buffer{}, &prompt, spec)
	if err == nil || !strings.Contains(err.Error(), "type the node name "+testMachineName+" (node "+host+")") {
		t.Fatalf("the hostname must not confirm: %v", err)
	}

	if !strings.Contains(prompt.String(), "Type the node name "+testMachineName+" (node "+host+") to confirm") || !present(k8s, testMachineName) {
		t.Fatalf("the prompt names the machine and the node:\n%s", prompt.String())
	}

	spec.Confirm = host
	if err := runNodeAction(ctx, c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, spec); err == nil ||
		!strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("--confirm with the hostname must be refused: %v", err)
	}

	spec.Confirm = testMachineName
	if err := runNodeAction(ctx, c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, spec); err != nil {
		t.Fatalf("--confirm with the machine name: %v", err)
	}
}

func TestNodeRemoveWipeMode(t *testing.T) {
	for flag, want := range map[string]string{
		"": "", wipeModeSystemPartitions: "", ichorv1alpha1.WipeModeSystemDisk: ichorv1alpha1.WipeModeSystemDisk,
		ichorv1alpha1.WipeModeAll: ichorv1alpha1.WipeModeAll,
	} {
		got, err := wipeModeWire(flag)
		if err != nil || got != want {
			t.Errorf("wipeModeWire(%q) = %q, %v; want %q", flag, got, err, want)
		}
	}

	if _, err := wipeModeWire("everything"); err == nil || !strings.Contains(err.Error(), "--wipe-mode") {
		t.Fatalf("an unknown mode = %v", err)
	}

	c, _ := nodeActionClients(t, false)
	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName, WipeMode: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "--wipe-mode") {
		t.Fatalf("a bad mode must stop before anything is written: %v", err)
	}

	var errw bytes.Buffer
	if err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw,
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_REMOVE, Confirm: testMachineName, WipeMode: ichorv1alpha1.WipeModeSystemDisk}); err != nil {
		t.Fatalf("system-disk remove: %v", err)
	}

	if !strings.Contains(errw.String(), "whole system disk") {
		t.Fatalf("the plan and the next-steps text must say the whole disk is wiped:\n%s", errw.String())
	}
}

// all erases the same system disk as system-disk, so neither the help nor the flag may say it also wipes user disks.
func TestNodeRemoveAllDoesNotClaimUserDisks(t *testing.T) {
	usage := deleteNodeCmd.Flags().Lookup("wipe-mode").Usage
	if !strings.Contains(nodeRemoveLong, "never a user disk") || strings.Contains(nodeRemoveLong, "every user disk") {
		t.Fatalf("the help for all must say it never wipes a user disk:\n%s", nodeRemoveLong)
	}

	if !strings.Contains(usage, "user disks are not wiped") || strings.Contains(usage, "every user disk") {
		t.Fatalf("the --wipe-mode usage for all must say user disks are not wiped: %q", usage)
	}
}
