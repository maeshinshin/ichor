package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func pendingPod(msg string) *corev1.Pod {
	p := &corev1.Pod{
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable, Message: msg},
		}},
	}
	p.Namespace, p.Name = ichorControllerNamespace, "ichor-pending"

	return p
}

func nodeWithReady(status corev1.ConditionStatus, msg string) *corev1.Node {
	n := &corev1.Node{
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: status, Reason: "KubeletNotReady", Message: msg},
		}},
	}
	n.Name = "n1"

	return n
}

func TestWaitDiagPrintsPendingReasonEvery30s(t *testing.T) {
	const msg = "0/3 nodes are available: 3 node(s) had untolerated taint"

	c := fake.NewClientBuilder().WithObjects(pendingPod(msg), nodeWithReady(corev1.ConditionTrue, "")).Build()

	var out bytes.Buffer

	now := time.Unix(0, 0)
	d := newWaitDiag(c, &out)
	d.now = func() time.Time { return now }

	_ = d.tick(context.Background())
	_ = d.tick(context.Background()) // same instant: not printed again

	if got := strings.Count(out.String(), msg); got != 1 {
		t.Fatalf("printed %d times, want 1:\n%s", got, out.String())
	}

	now = now.Add(31 * time.Second)
	_ = d.tick(context.Background())

	if got := strings.Count(out.String(), msg); got != 2 {
		t.Fatalf("printed %d times after 31s, want 2:\n%s", got, out.String())
	}
}

func TestWaitDiagFailsEarlyOnNotReadyNodes(t *testing.T) {
	c := fake.NewClientBuilder().WithObjects(
		pendingPod("0/1 nodes are available"),
		nodeWithReady(corev1.ConditionFalse, "container runtime network not ready: cni plugin not initialized"),
	).Build()

	now := time.Unix(0, 0)
	d := newWaitDiag(c, &bytes.Buffer{})
	d.now = func() time.Time { return now }

	if err := d.tick(context.Background()); err != nil {
		t.Fatalf("failed at once: %v", err)
	}

	now = now.Add(waitDiagGiveUp - time.Second)
	if err := d.tick(context.Background()); err != nil {
		t.Fatalf("failed before 2 minutes: %v", err)
	}

	now = now.Add(2 * time.Second)
	if err := d.tick(context.Background()); !errors.Is(err, errNoPodNetwork) {
		t.Fatalf("got %v, want errNoPodNetwork", err)
	}
}

func TestWaitDiagKeepsWaitingWhileCiliumIsInstalling(t *testing.T) {
	agent := &corev1.Pod{}
	agent.Namespace, agent.Name, agent.Labels = k8sFactsSystemNamespace, "cilium-x", map[string]string{"k8s-app": "cilium"}

	c := fake.NewClientBuilder().WithObjects(
		pendingPod("0/1 nodes are available"), agent,
		nodeWithReady(corev1.ConditionFalse, "container runtime network not ready: cni plugin not initialized"),
	).Build()

	now := time.Unix(0, 0)
	d := newWaitDiag(c, &bytes.Buffer{})
	d.now = func() time.Time { return now }

	_ = d.tick(context.Background())

	now = now.Add(waitDiagGiveUp + time.Minute)
	if err := d.tick(context.Background()); err != nil {
		t.Fatalf("gave up while Cilium's agent exists: %v", err)
	}
}

func TestWaitDiagKeepsWaitingWhenNodesAreReady(t *testing.T) {
	c := fake.NewClientBuilder().WithObjects(pendingPod("insufficient memory"), nodeWithReady(corev1.ConditionTrue, "")).Build()

	now := time.Unix(0, 0)
	d := newWaitDiag(c, &bytes.Buffer{})
	d.now = func() time.Time { return now }

	for range 5 {
		now = now.Add(time.Minute)

		if err := d.tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPollBoundedStopsEarlyAndTimesOutWithReason(t *testing.T) {
	notReady := fake.NewClientBuilder().WithObjects(
		pendingPod("0/1 nodes are available"),
		nodeWithReady(corev1.ConditionFalse, "cni plugin not initialized"),
	).Build()

	now := time.Unix(0, 0)
	d := newWaitDiag(notReady, &bytes.Buffer{})
	d.now = func() time.Time { return now }

	errNot := errors.New("not yet")
	attempt := func(context.Context) error {
		now = now.Add(time.Minute) // each attempt is one fake minute

		return errNot
	}

	err := pollDiag(context.Background(), time.Hour, time.Millisecond, &bytes.Buffer{}, "x", d, attempt, func(error) bool { return true }, "x did not finish")
	if !errors.Is(err, errNoPodNetwork) {
		t.Fatalf("got %v, want errNoPodNetwork", err)
	}

	ready := fake.NewClientBuilder().WithObjects(pendingPod("insufficient memory"), nodeWithReady(corev1.ConditionTrue, "")).Build()
	d = newWaitDiag(ready, &bytes.Buffer{})

	err = pollDiag(context.Background(), 30*time.Millisecond, time.Millisecond, &bytes.Buffer{}, "Ichor's webhooks", d,
		func(context.Context) error { return errNot }, func(error) bool { return true }, "Ichor's webhooks did not finish within 30ms")
	if !errors.Is(err, errNot) || !strings.Contains(err.Error(), "did not finish within") || !strings.Contains(err.Error(), "insufficient memory") {
		t.Fatalf("got %v", err)
	}
}
