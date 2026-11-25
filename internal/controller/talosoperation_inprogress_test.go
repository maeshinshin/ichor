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

package controller

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubefake "k8s.io/client-go/kubernetes/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func named(names ...string) []*ichorv1alpha1.TalosMachine {
	out := make([]*ichorv1alpha1.TalosMachine, 0, len(names))
	for _, n := range names {
		m := &ichorv1alpha1.TalosMachine{}
		m.Name = n
		out = append(out, m)
	}

	return out
}

func namesOf(ms []*ichorv1alpha1.TalosMachine) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}

	return out
}

func inProgress(names ...string) []ichorv1alpha1.TalosOperationMachineStatus {
	out := make([]ichorv1alpha1.TalosOperationMachineStatus, 0, len(names))
	for _, n := range names {
		out = append(out, ichorv1alpha1.TalosOperationMachineStatus{Name: n, Phase: ichorv1alpha1.TalosOperationMachinePhaseInProgress})
	}

	return out
}

func TestNextBatchFinishesTheInProgressControlPlaneFirst(t *testing.T) {
	got := nextBatch(named("a", "b"), nil, nil, inProgress("b"), 1)
	if g := namesOf(got); len(g) != 1 || g[0] != "b" {
		t.Fatalf("got %v, want [b]", g)
	}
}

func TestNextBatchFinishesTheInProgressWorkerFirst(t *testing.T) {
	got := nextBatch(nil, named("w0", "w1"), nil, inProgress("w1"), 1)
	if g := namesOf(got); len(g) != 1 || g[0] != "w1" {
		t.Fatalf("got %v, want [w1]", g)
	}
}

func TestNextBatchFillsOnlyTheRemainingWorkerSlots(t *testing.T) {
	got := namesOf(nextBatch(nil, named("w0", "w1", "w2"), nil, inProgress("w1"), 2))
	if len(got) != 2 || got[0] != "w1" || got[1] != "w0" {
		t.Fatalf("got %v, want [w1 w0]", got)
	}
}

func TestConfigSelectionKeepsAMachineWhoseRebootWasRequested(t *testing.T) {
	op := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindConfig}}
	m := named("cp")[0]
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionConfigRollout, Status: metav1.ConditionTrue, Reason: reasonRolloutRebooting,
	})

	cps, _ := configSelection(op, []*ichorv1alpha1.TalosMachine{m}, nil)
	if len(cps) != 1 {
		t.Fatalf("the machine with a ConfigRollout marker was dropped")
	}

	m.Status.Conditions = nil
	if cps, _ = configSelection(op, []*ichorv1alpha1.TalosMachine{m}, nil); len(cps) != 0 {
		t.Fatalf("a machine with nothing pending and no marker must be dropped")
	}
}

var _ = Describe("TalosOperationReconciler pausing mid-step", func() {
	It("keeps the InProgress entry when a spec write lands during the step", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "pause-cp")
		op := newOpTestOperation(cluster.Name)

		r, _ := newOpTestReconciler(nil)
		finish := false
		r.Advance = func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation) (bool, error) {
			if finish {
				return true, nil
			}

			var live ichorv1alpha1.TalosOperation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &live)).To(Succeed())
			live.Spec.Paused = true
			Expect(k8sClient.Update(ctx, &live)).To(Succeed())

			return false, nil
		}

		got := reconcileFetch(r, op)
		Expect(got.Status.Machines).To(ConsistOf(HaveField("Name", cp.Name)))
		Expect(got.Status.Machines[0].Phase).To(Equal(ichorv1alpha1.TalosOperationMachinePhaseInProgress))

		got.Spec.Paused = false
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		finish = true
		mustReconcileOperation(r, op)

		got = reconcileFetch(r, op)
		Expect(got.Status.Machines[0].Phase).To(Equal(ichorv1alpha1.TalosOperationMachinePhaseCompleted))
	})
})

var _ = Describe("TalosOperationReconciler failing while paused", func() {
	It("keeps the InProgress entry when a step fails while a spec write lands", func() {
		cluster := newOpTestCluster()
		cp := newOpTestMachine(cluster.Name, "controlplane", "failpause-cp")
		op := newOpTestOperation(cluster.Name)

		r, _ := newOpTestReconciler(nil)
		r.Advance = func(_ context.Context, _ *ichorv1alpha1.TalosCluster, _ *ichorv1alpha1.TalosMachine, _ *ichorv1alpha1.TalosOperation) (bool, error) {
			var live ichorv1alpha1.TalosOperation
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &live)).To(Succeed())
			live.Spec.Paused = true
			Expect(k8sClient.Update(ctx, &live)).To(Succeed())

			return false, errors.New("boom")
		}

		got := reconcileFetch(r, op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.Machines).To(ConsistOf(HaveField("Name", cp.Name)))
		Expect(got.Status.Machines[0].Phase).To(Equal(ichorv1alpha1.TalosOperationMachinePhaseInProgress))
	})
})

var _ = Describe("a config rollout paused after its reboot was requested", func() {
	It("resumes the rebooted node, uncordons it and completes", func() {
		f, m := newRolloutNode("node-pause-resume", true)
		m.Status.RebootPaths = []string{"machine.install"}
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

		ready := corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}
		// A second schedulable node, or the drain is skipped as pointless.
		kube := kubefake.NewClientset(
			&corev1.Node{Name: "node-pause-resume", Status: ready},
			&corev1.Node{Name: "node-pause-other", Status: ready},
		)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		adv := newRolloutAdvancer(fake)
		adv.Kube = kube

		op := newOpTestOperationOfKind(f.cluster.Name, ichorv1alpha1.TalosOperationKindConfig)
		r, _ := newOpTestReconciler(nil)
		r.Advance = adv.Advance

		got := reconcileFetch(r, op)
		Expect(fake.rebootCalls).To(Equal(1))
		Expect(got.Status.Machines).To(ConsistOf(HaveField("Phase", ichorv1alpha1.TalosOperationMachinePhaseInProgress)))

		node, err := kube.CoreV1().Nodes().Get(ctx, "node-pause-resume", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(node.Spec.Unschedulable).To(BeTrue(), "Ichor cordoned the node before the reboot")
		Expect(node.Annotations).To(HaveKey(cordonedByIchorAnnotation))

		// The pause lands and the status mark is lost, as a crash before the write would lose it.
		got.Status.Machines = nil
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		got = fetchOperation(op)
		got.Spec.Paused = true
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		mustReconcileOperation(r, op)

		// The node reboots onto the staged configuration, so nothing is pending any more.
		fake.bootID = testBootIDAfter
		fake.current = f.after(m)

		live := fetchMachine(m)
		live.Status.RebootPaths = nil
		Expect(k8sClient.Status().Update(ctx, live)).To(Succeed())

		got = fetchOperation(op)
		got.Spec.Paused = false
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		reconcileTimes(r, op, 3)

		Expect(fake.rebootCalls).To(Equal(1), "the resumed node is not rebooted again")
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		node, err = kube.CoreV1().Nodes().Get(ctx, "node-pause-resume", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(node.Spec.Unschedulable).To(BeFalse())
		Expect(node.Annotations).NotTo(HaveKey(cordonedByIchorAnnotation))
		Expect(node.Annotations).NotTo(HaveKey(drainStartedAnnotation))
	})
})
