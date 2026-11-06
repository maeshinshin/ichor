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
	apiOpts.Override = true // a remove and a reset both need it

	k8s := reviewAs(true, fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(opTestObjects()...).Build())

	return localClients(apiOpts, apiDeps{K8s: k8s}), k8s
}

// present reports whether the machine is still there.
func present(k8s client.Client, name string) bool {
	var m ichorv1alpha1.TalosMachine

	return k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &m) == nil
}

func TestNodeCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"reboot", "shutdown", "reset", "remove", "restart-service"} {
		if c, _, err := nodeCmd.Find([]string{name}); err != nil || c == nil || c.Name() != name {
			t.Errorf("ichor node %s is not registered: %v", name, err)
		}
	}

	for _, name := range []string{"defrag", "forfeit-leadership"} {
		if c, _, err := etcdCmd.Find([]string{name}); err != nil || c == nil || c.Name() != name {
			t.Errorf("ichor etcd %s is not registered: %v", name, err)
		}
	}
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

func TestNodeResetNeedsOverride(t *testing.T) {
	c, _ := nodeActionClients(t, true)
	apiOpts.Override = false

	err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_RESET, Confirm: testMachineName})
	if err == nil || !strings.Contains(err.Error(), "--override") {
		t.Fatalf("reset without override = %v", err)
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

	if !strings.Contains(done.String(), "deletes its Kubernetes Node") || !strings.Contains(done.String(), "new pending machine") {
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
// consent, so it does not also ask "Proceed? [y/N]" (restore --live and machine reclaim treat it alike).
func TestNodeResetAndRemoveWithConfirmDoNotAskAgain(t *testing.T) {
	for _, action := range []pb.NodeAction{pb.NodeAction_NODE_ACTION_RESET, pb.NodeAction_NODE_ACTION_REMOVE} {
		c, _ := nodeActionClients(t, false)
		apiOpts.Override = true

		var errw bytes.Buffer

		err := runNodeAction(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &errw,
			nodeActionSpec{Machine: testMachineName, Action: action, Confirm: testMachineName})
		// The fake API cannot answer reset's SelfSubjectAccessReview, so only the question matters here.
		if err != nil && (strings.Contains(err.Error(), "aborted") || strings.Contains(err.Error(), "reading confirmation")) {
			t.Fatalf("%v: --confirm alone must be enough: %v\n%s", action, err, errw.String())
		}

		if strings.Contains(errw.String(), "Proceed?") || errw.Len() == 0 {
			t.Fatalf("%v: the plan is shown and nothing is asked:\n%s", action, errw.String())
		}
	}
}

// --wipe-mode belongs to reset alone and reaches the request.
func TestNodeResetWipeModeFlag(t *testing.T) {
	if nodeResetCmd.Flags().Lookup("wipe-mode") == nil {
		t.Fatal("ichor node reset has no --wipe-mode")
	}

	if nodeRemoveCmd.Flags().Lookup("wipe-mode") != nil || nodeRebootCmd.Flags().Lookup("wipe-mode") != nil {
		t.Fatal("--wipe-mode is for reset only")
	}

	req := nodeActionSpec{Machine: testMachineName, Action: pb.NodeAction_NODE_ACTION_RESET, WipeMode: "system-disk"}.request(nil)
	if req.GetWipeMode() != "system-disk" {
		t.Fatalf("request = %+v", req)
	}
}

// ichor node reset is the deprecated alias of node remove --reboot.
func TestNodeRemoveHasRebootAndResetIsDeprecated(t *testing.T) {
	if nodeRemoveCmd.Flags().Lookup("reboot") == nil {
		t.Error("ichor node remove has no --reboot")
	}

	if !strings.Contains(nodeResetCmd.Short, "Deprecated") || !strings.Contains(nodeResetCmd.Short, "node remove --reboot") {
		t.Errorf("node reset must point at node remove --reboot: %q", nodeResetCmd.Short)
	}
}
