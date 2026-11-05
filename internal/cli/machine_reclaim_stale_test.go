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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

func staleMachine(name, uuid, state string) *ichorv1alpha1.TalosMachine {
	return &ichorv1alpha1.TalosMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"}, Identity: ichorv1alpha1.TalosMachineIdentity{UUID: uuid},
			ApprovalState: state,
		},
	}
}

// The reclaim resets the node, so the pending TalosMachine that pinned the old maintenance
// certificate is stale: it is deleted (the cluster's discovery creates it again with the new pin). An
// approved machine, a rejected one and another node's are never touched.
func TestClearStalePendingDeletesOnlyThePendingMachineOfTheUUID(t *testing.T) {
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(
		staleMachine("aaaa-1", "aaaa-1", ichorv1alpha1.MachineApprovalStatePending),
		staleMachine("rejected", "AAAA-1", ichorv1alpha1.MachineApprovalStateRejected),
		staleMachine("approved", "AAAA-1", ichorv1alpha1.MachineApprovalStateApproved),
		staleMachine("other", "bbbb-2", ichorv1alpha1.MachineApprovalStatePending),
	).Build()

	names, err := clearStalePending(context.Background(), k8s, testNamespace, "AAAA-1")
	if err != nil {
		t.Fatal(err)
	}

	if len(names) != 1 || names[0] != "aaaa-1" {
		t.Fatalf("deleted %v, want [aaaa-1]", names)
	}

	var left ichorv1alpha1.TalosMachineList
	if err := k8s.List(context.Background(), &left); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, m := range left.Items {
		got[m.Name] = true
	}

	if got["aaaa-1"] || !got["rejected"] || !got["approved"] || !got["other"] || len(got) != 3 {
		t.Fatalf("machines left: %v", got)
	}
}

func TestMachineReclaimClearsTheStalePinAfterTheReset(t *testing.T) {
	h := newReclaimHarness("", reclaim.Known{})

	var cleared []string

	h.deps.ClearStale = func(_ context.Context, uuid string) ([]string, error) {
		if len(h.node.resets) != 1 {
			t.Error("the stale machine was cleared before the reset was sent")
		}

		cleared = append(cleared, uuid)

		return []string{"aaaa-1"}, nil
	}

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID, Wait: time.Second}); err != nil {
		t.Fatal(err)
	}

	if len(cleared) != 1 || cleared[0] != testUUID {
		t.Fatalf("cleared = %v", cleared)
	}

	if !strings.Contains(h.out.String(), "aaaa-1") || !strings.Contains(h.out.String(), "maintenance certificate") {
		t.Fatalf("the output does not say what was cleared and why:\n%s", h.out.String())
	}
}

// If the stale machine cannot be removed (no permission), the reset has happened already: the reclaim
// goes on and prints the exact command.
func TestMachineReclaimStaleClearFailureIsAWarningWithTheCommand(t *testing.T) {
	h := newReclaimHarness("", reclaim.Known{})
	h.deps.ClearStale = func(context.Context, string) ([]string, error) {
		return nil, errors.New("forbidden")
	}

	if err := runMachineReclaim(context.Background(), h.deps, "10.0.0.5", reclaimFlags{Confirm: testUUID, Wait: time.Second}); err != nil {
		t.Fatalf("a failure to clear the stale machine failed the reclaim: %v", err)
	}

	out := strings.ToLower(h.out.String())
	for _, want := range []string{"forbidden", "kubectl", "talosmachines", strings.ToLower(testUUID)} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, h.out.String())
		}
	}
}
