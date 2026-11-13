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

	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// `ichor rollout config` starts a config operation through OperationService like every other start: the plan names the
// machines and the fields that need the reboot, and
// nothing reboots without the operation.

func configApplyObjects() []client.Object {
	objs := opTestObjects()

	for _, o := range objs {
		if m, ok := o.(*ichorv1alpha1.TalosMachine); ok && m.Name == opCP {
			m.Status.RebootPaths = []string{"cluster.proxy.disabled"}
		}
	}

	return objs
}

func TestConfigApplyPromptNamesTheMachineTheFieldAndTheRebootAndDeclineWritesNothing(t *testing.T) {
	setAPITestFlags(t, false)

	c, k8s := freshClients(t, configApplyObjects())

	var prompt bytes.Buffer

	err := runStartOperation(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &prompt,
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: opTestCluster})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("want aborted, got %v", err)
	}

	for _, want := range []string{"(config)", "1 machine(s)", "1. " + opCP + " (controlplane)", "cluster.proxy.disabled", "REBOOT"} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt.String())
		}
	}

	if len(opTestOps(t, k8s)) != 0 {
		t.Fatal("a declined rollout config wrote something")
	}
}

func TestConfigApplyWithYesCreatesOneConfigOperation(t *testing.T) {
	setAPITestFlags(t, true)

	c, k8s := freshClients(t, configApplyObjects())

	if err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: opTestCluster}); err != nil {
		t.Fatal(err)
	}

	ops := opTestOps(t, k8s)
	if len(ops) != 1 || ops[0].Spec.Kind != ichorv1alpha1.TalosOperationKindConfig {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestConfigApplyRefusesWhenNothingNeedsAReboot(t *testing.T) {
	setAPITestFlags(t, true)

	c, k8s := freshClients(t, opTestObjects())

	err := runStartOperation(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{},
		startSpec{Kind: pb.OperationKind_OPERATION_KIND_CONFIG, Cluster: opTestCluster})
	if err == nil || len(opTestOps(t, k8s)) != 0 {
		t.Fatalf("an empty rollout config must be refused and write nothing: %v", err)
	}
}

func TestConfigApplyIsAWriteAndNamedConfig(t *testing.T) {
	if _, ok := rolloutConfigCmd.Annotations[writesAnnotation]; !ok {
		t.Error("rollout config is not marked as a write")
	}

	if got := kindName(pb.OperationKind_OPERATION_KIND_CONFIG); got != "config" {
		t.Errorf("kindName = %q", got)
	}
}
